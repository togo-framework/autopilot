# Autopilot security model

Status: **draft for maintainer approval** · tracks [togo-framework/autopilot#1](https://github.com/togo-framework/autopilot/issues/1)

Autopilot is not a normal plugin. It stores user-supplied text, hands part of it
to an autonomous coding agent, and lets that agent edit a working tree and push a
branch. This document says what we protect, who we trust, and what must be enforced before Autopilot is recommended
or enabled by default.

This is a design document. "Required" sections are the acceptance bar for the
hardening release; the regression tests in section 5 are how we prove it.

## 1. What we protect

| Asset | Why it matters |
|---|---|
| The repository checkout and its branches | The agent writes here; a bad change can become a PR a human trusts. |
| Credentials reachable by the runner | The git/`gh` token, the Claude Code login, and the process environment (database URL, API keys). |
| Issue, comment and attachment content | May contain customer data; is also prompt input for the agent. |
| The host the runner runs on | The agent can execute whatever Claude Code and its settings allow. |
| Other users of the app | Stored content is rendered in other users' browsers (the board, the SDK toasts). |

## 2. Actors

| Actor | Trust |
|---|---|
| Anonymous visitor | None. May reach only the feedback ingress. |
| Signed-in app user (for example a customer) | Low. Being logged in to the app is **not** the same as being allowed to operate Autopilot. |
| Autopilot operator (staff) | Trusted to triage, hand issues to the agent, and accept results. |
| Maintainer / admin | Trusted to enable the runner, set the working directory, and hold the deploy credentials. |
| The agent (Claude Code and its tools) | **Untrusted.** It follows instructions found in issue text, so treat its output as unreviewed input. |

## 3. Trust boundaries

```
 anonymous ──► /api/autopilot/feedback ─────────────┐
 app user  ──► /api/autopilot/* (session) ──────────┤  autopilot_* tables
 staff     ──► /autopilot board (session) ──────────┘        │
                                                              ▼  title + body
                                                     runner (opt-in)
                                                              │  claude -p ... , git, gh
                                                              ▼
                                                  working tree  ──►  branch / PR  ──►  human review
```

Three boundaries matter:

1. **Public input → stored issue.** Anything an anonymous caller or an app user
   writes is attacker-controlled.
2. **Stored issue → agent prompt.** `buildPrompt` copies the issue title and body
   verbatim into the prompt. Prompt injection is the central threat.
3. **Agent → repository and network.** What the agent process can read, run and
   reach is the blast radius of a successful injection.

## 4. Review scope, item by item

Each item from the hardening checklist is answered with **Required** (the bar for
the hardening release) and **Tests** (the bypass regression tests that prove it).

### 4.1 Authorization for reading and filing issues

**Required.**
- Fail closed. If the `auth` plugin is absent the API must refuse to mount, unless
  an explicit `AUTOPILOT_ALLOW_UNAUTHENTICATED=1` is set, and that option logs a
  warning at boot and is documented as development-only.
- Introduce roles checked **server side** on every handler: `viewer` (read),
  `reporter` (create `feedback`/`backlog` issues, comment on own issues), and
  `operator` (change status, hand to agent, edit any issue, upload).
  A signed-in user with no Autopilot role gets 403, not 200.
- Only `operator` may set `status` to `ready`, accept `in_review → done`, change
  `human_only`, or reset a claim. `createIssue` ignores a client-supplied `status`
  from anyone else and forces `backlog`.
- Reject unknown `kind` and `priority` values with a fixed allow-list.
- Every query that returns issues applies the caller's scope (see 4.9).

**Tests.** Unauthenticated, signed-in-without-role, `reporter` and `viewer`
callers each attempt: list, read, create with `status=ready`, patch, status
change, comment, upload, WebSocket connect. All must be refused or downgraded as
specified. A boot test proves the API does not mount without `auth`.

### 4.2 Feedback submission

**Required.** Decision: **keep it public, but make it cheap to abuse-proof.**
- `http.MaxBytesReader` (suggested 16 KiB) and per-field length limits.
- Rate limit per client IP and a global ceiling, for example 5 per minute per IP
  and 120 per hour overall, answering 429. The limiter is in memory by default and
  pluggable for multi-instance deployments.
- Optional `AUTOPILOT_FEEDBACK_ORIGINS` allow-list checked against `Origin`.
  Default for browser calls from a different origin is deny.
- Optional shared-secret or signed-token mode for apps that want to restrict the
  endpoint to their own pages.
- `created_by` for public submissions is always the literal `feedback`; the
  caller-supplied `user` is stored in a separate, clearly untrusted field.
- Feedback issues are labelled **untrusted** (see 4.4) and can never be handed to
  the agent without an operator editing or confirming them.
- An off switch: `AUTOPILOT_FEEDBACK=off`.

**Tests.** Oversized payload → 413. Burst from one IP → 429. Disallowed origin →
403. A feedback issue cannot be moved to `ready` by the caller or by any
non-operator.

### 4.3 Permissions and auditability

**Required.**
- The actor is derived from the session, never from the body. `author_kind` is set
  by the server (`human` for a session user, `agent` only for the runner, `system`
  for self-heal and feedback).
- An append-only `autopilot_audit` table: `id, at, actor, actor_kind, action,
  issue_id, from_status, to_status, detail, remote_addr`. Written in the same
  transaction as the change. Covers create, edit, status change, comment, upload,
  claim, runner start/finish, push, PR creation, and every denied privileged
  request.
- The board shows an issue's audit trail. The audit table has no update or delete
  path in the plugin.

**Tests.** A request that sends `author="admin"`, `author_kind="agent"` is stored
with the session's identity. Every transition produces exactly one audit row, and
a denied transition produces a denial row.

### 4.4 Claude Code execution mode (threat model for the runner)

**Threats.**
- *Prompt injection.* Issue text, and any file or web page the agent reads while
  working, can tell it to change unrelated files, add a dependency, weaken a check,
  exfiltrate a secret into a comment or a commit, or edit CI configuration.
- *Injection through automation.* Self-heal files issues whose title and body
  contain the request method and path of a failing request. An attacker who can
  cause a 5xx controls part of that text. With `AUTOPILOT_SELF_HEAL_READY=1` that
  text goes to the agent with no human in between.
- *Ambient authority.* The agent runs with the app's environment variables and the
  checkout's git credentials.
- *Output as an attack.* A diff that looks plausible but is hostile, relying on a
  hurried reviewer.

**Required.**
- Default execution backend is an **isolated workspace** (container or the `coder`
  provider), not the host's working tree. The in-place `local` backend stays
  available but logs a warning and requires `AUTOPILOT_ALLOW_LOCAL_EXEC=1`.
- Run the agent with a **scrubbed environment**: an explicit allow-list (`PATH`,
  `HOME` of a dedicated user, the Claude login), never the app's own variables.
- Pass an explicit tool policy to Claude Code (allowed and disallowed tools) and do
  not rely on the repository's settings. No `bypassPermissions`, ever.
- Only issues with `trust = operator-confirmed` can be claimed. Content authored by
  anonymous or low-role users is shown to the operator with the exact text that
  will reach the agent, and the operator confirms it.
- Self-heal never files directly to `ready`. `AUTOPILOT_SELF_HEAL_READY` is
  removed, or it becomes an operator-confirmed shortcut only. Sanitise and truncate
  path and message text before they are stored.
- Add a diff gate before commit: refuse (move to `blocked`) when the change touches
  protected paths (CI workflows, `.git*`, `.claude/`, lock and build files,
  `.env*`, anything matching `AUTOPILOT_PROTECTED_PATHS`) or exceeds a file or line
  limit.
- Never run `git add -A` blindly: stage only paths that pass the gate.

**Tests.** A fake implementer that writes to a protected path, writes a file
outside the workdir, and returns oversized output: each ends in `blocked`, none
reaches a commit. A test proves the child environment contains none of the app's
variables. A prompt-injection fixture issue that asks for a secret cannot cause
the secret value to appear in any stored comment or result.

### 4.5 Repository access boundaries

**Required.**
- Fixed allow-list in configuration: one repository, a base branch, and a branch
  prefix (`autopilot/`). The runner refuses to start if `origin` does not match
  the configured repository.
- Pushes only to `autopilot/*` refs, never to the base branch or any protected
  branch; PRs only target the configured base. Never force-push.
- Branch names derive from the full issue id, and the runner never deletes a branch
  it did not create (tracked in the issue row).
- Use a **scoped deploy key or fine-grained token** limited to the one repository
  with contents and pull-request write only. Document branch protection on the
  base branch as a prerequisite.
- Clean working tree is required before a run; a dirty tree moves the issue to
  `blocked` instead of mixing changes.

**Tests.** A runner pointed at a mismatched remote refuses to start. A push to a
non-`autopilot/` ref and a force push are rejected by the runner before git runs.

### 4.6 Command and tool execution boundaries

**Required.**
- Sandboxing comes from the isolated backend in 4.4: no host mounts, a
  non-root user, CPU, memory and wall-clock limits.
- Network egress default **deny**, with an explicit allow-list (the git host, the
  package registry the project needs, the Claude API). No access to cloud metadata
  addresses or the app's internal network.
- The set of commands the runner itself runs is fixed in code (`git`, `gh`, the
  configured agent binary). `AUTOPILOT_CLAUDE_BIN` must be an absolute path and is
  validated at start.
- Output captured from the agent is size-limited before it is stored or posted.

**Tests.** Egress to a non-listed host fails inside the backend. An oversized
agent output is truncated.

### 4.7 Explicit opt-in and approval

**Required.**
- Two explicit switches, both off by default: the plugin being installed, and
  `AUTOPILOT_RUNNER=1`. Neither is implied by the other.
- Handing an issue to the agent (`→ ready`) is an `operator` action recorded in the
  audit log (4.3).
- Code reaches the base branch only through a PR reviewed by a human who did not
  hand the issue over, enforced by branch protection (a documented prerequisite,
  checked best-effort at start when a token is available).
- The agent never merges, never deploys, never edits repository settings or
  secrets. `autopilot` has no code path that does.

**Tests.** A `reporter` cannot reach `ready`. An `operator` handoff produces an
audit row. There is no exported function or route that merges or deploys.

### 4.8 Secrets exposure

**Required.**
- Scrubbed agent environment (4.4); the runner token for git/`gh` is mounted only
  into the isolated workspace.
- A redaction pass over text stored from the agent (comments, `result`, PR body)
  and over logs: known token shapes and any value of a configured secret name.
- Warn on likely secrets in submitted issue text and refuse to hand such an issue
  to the agent until an operator edits it.
- Serve attachments with a fixed safe type, `X-Content-Type-Options: nosniff` and
  `Content-Disposition: attachment` for anything that is not an image or video on
  an allow-list; sanitise the filename; add `Content-Security-Policy: sandbox`.
- Do not include secrets in the PR body: the PR body is the issue body, so the
  redaction above applies there too.

**Tests.** An uploaded `text/html` file is served as a download with `nosniff` and
never executes on the app origin. A planted token in an agent output is redacted
before storage.

### 4.9 Tenant and user isolation

**Required.** Decide per deployment mode:
- *Single-tenant staff tool* (the intended use): isolation is by role (4.1); no
  tenant column needed, and customers must not be given an Autopilot role.
- *Multi-tenant app*: add `tenant_id` and `owner_id` to all three tables, apply
  them in every query, and key the WebSocket stream and attachment routes by
  tenant. Until that exists the plugin must state that it is **not safe** for
  multi-tenant apps.

**Tests.** With two tenants, a user of tenant A cannot list, read, comment on,
download from, or receive WebSocket events for tenant B.

### 4.10 Safe defaults

**Required.** Off unless explicitly enabled, least privilege, and **not in the
`create-togo-app` / `togo new` defaults** until this model is approved and the
tests in section 5 pass. Concretely:

| Setting | Default |
|---|---|
| Plugin mounted | only when the app opts in |
| Auth plugin required | yes (fail closed) |
| Roles | no Autopilot role is granted implicitly |
| Feedback endpoint | off until `AUTOPILOT_FEEDBACK=on`, with limits |
| Runner | off |
| Execution backend | isolated workspace |
| Push and PR creation | off |
| Self-heal | off, never straight to `ready` |
| WebSocket origins | same origin only |

## 5. Regression tests required for approval

Not only happy paths. The hardening release is blocked until these exist and pass
in CI (`go test ./...`, no network, SQLite in memory):

1. Unauthenticated caller against every route, including WebSocket and uploads.
2. Signed-in user without an Autopilot role against every route.
3. `reporter` and `viewer` attempting privileged transitions, `status=ready` on
   create, `human_only` changes, and claim reset.
4. Spoofed `author`, `author_kind`, `created_by` ignored.
5. Feedback abuse: oversized body, burst, disallowed origin.
6. Stored-XSS payloads in every free-text field that reaches the browser.
7. Cross-origin WebSocket handshake refused.
8. Attachment with an active content type is not rendered inline.
9. Runner: protected-path edit, out-of-workdir write, oversized diff, mismatched
   remote, push to a protected ref, dirty tree. All end in `blocked`.
10. Agent environment contains none of the host's variables.
11. Self-heal cannot create a `ready` issue and strips control text from paths.
12. Audit row exists for every transition and every denial.

## 6. Decisions for the maintainer

1. **Default inclusion.** Recommendation: **do not include Autopilot in
   `create-togo-app` / `togo new` by default.** Offer it as an explicit opt-in
   (for example `togo install togo-framework/autopilot`, never implicit) that
   prints the safe-default table above. Revisit after the hardening release and
   the section 5 tests are green.
2. **Feedback endpoint.** Keep it public with the controls in 4.2, or require a
   token for it. Recommendation: public with controls, off until enabled.
3. **Roles.** Own `viewer / reporter / operator` roles in the plugin, or map to the
   `auth` plugin's existing roles. Recommendation: map to `auth` roles if it has
   them, otherwise a minimal table owned by this plugin.
4. **Execution backend.** Make the isolated backend the default and require the
   explicit flag for in-place local runs. Recommendation: yes.
5. **Multi-tenant support.** Declare unsupported for now (4.9) or schedule the
   tenant columns. Recommendation: declare unsupported and say so in the README.

## 7. Plan

Documentation first (this change), then small PRs, each with its bypass tests:
fail-closed auth and roles; actor from the session and the audit table; input
limits, feedback controls and the XSS and WebSocket fixes; the runner
(environment scrub, tool policy, diff gate, repository and branch limits); then
the release with notes. Any advisory is coordinated through private disclosure per
[SECURITY.md](../SECURITY.md).
