# Autopilot security model

Status: **draft for maintainer approval** · tracks [togo-framework/autopilot#1](https://github.com/togo-framework/autopilot/issues/1)

Implementation status is marked below as of **main 6cd5f42** (hardening PRs #3 to #8 merged). **done (#N)** means the merged PR implements it; **open** means it is not in `main` yet. This is a documentation update only: no behaviour changes here, and the five decisions in section 6 are still the owner's.

Autopilot is not a normal plugin. It stores user-supplied text, hands part of it
to an autonomous coding agent, and lets that agent edit a working tree and push a
branch. This document says what we protect, who we trust, and what must be enforced before Autopilot is recommended
or enabled by default.

This is a design document. "Required" sections are the acceptance bar for the
hardening release; the regression tests in section 5 are how we prove it.

## Hardening progress (as of main 6cd5f42)

| PR | What it did | Sections |
|---|---|---|
| [#3](https://github.com/togo-framework/autopilot/pull/3) | Require the auth plugin, fail closed; the dev-only opt-out is refused with the runner or a production environment | 4.1, 4.10 |
| [#4](https://github.com/togo-framework/autopilot/pull/4) | Same-origin check for the WebSocket stream (`AUTOPILOT_WS_ORIGINS` for extras) | 4.10, 5 (7) |
| [#5](https://github.com/togo-framework/autopilot/pull/5) | `kind` and `author_kind` allow-lists; the board and SDK escape quotes, only http(s) PR links | 4.1, 4.3, 5 (6) |
| [#6](https://github.com/togo-framework/autopilot/pull/6) | `viewer` / `reporter` / `operator` roles on every route; actor from the session; self-heal never files to `ready` | 4.1, 4.3, 4.4, 4.7 |
| [#7](https://github.com/togo-framework/autopilot/pull/7) | An open `/ws` is closed when the role is revoked; same-origin check on upload | 4.1, 4.8 |
| [#8](https://github.com/togo-framework/autopilot/pull/8) | Every write re-reads the account's current roles (`UserByID`); a PAT never gets the account's roles | 4.1 |

Not started: feedback abuse controls (4.2), the audit table (4.3), the runner hardening and isolation (4.4 to 4.6),
secrets redaction and upload headers (4.8), and the section 5 tests that depend on them.

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

**Status (main 6cd5f42).**
- done (#3): the API does not mount without the `auth` plugin. `AUTOPILOT_ALLOW_UNAUTHENTICATED=1` logs a warning, and is refused together with `AUTOPILOT_RUNNER=1` or a production `APP_ENV` / `TOGO_ENV` / `GO_ENV` / `ENV`.
- done (#6): roles `autopilot:viewer|reporter|operator` of the `auth` plugin, fixed names, no implicit role; a signed-in user without one gets 403 on every data route and on the WebSocket. A `reporter` creates `backlog` issues only and edits only the title and body of its own backlog issue. Only an `operator` changes `status`, assignee, priority, `human_only`, uploads, or unblocks by comment. Mutating routes require JSON (415 otherwise).
- done (#5): fixed allow-lists for `kind` and `author_kind`.
- done (#7, #8): an open `/ws` re-checks the account every 30 s and closes when the role, the account or the session is gone. Every write re-reads the account's current roles: a demoted operator gets 403, a deleted account 401, a store error 503, and a PAT keeps its own identity (no roles).
- open: a fixed allow-list for `priority`.
- open: per-caller scope on the queries (see 4.9; the plugin is single-tenant).

**Tests status.**
- done (#3, #6 to #8): boot without `auth`; no session, no role, `viewer`, `reporter` and `operator` against the routes and the WebSocket; real-auth tests (`roles_test.go`, `realauth_test.go`, `autopilot_test.go`).
- open: an explicit claim-reset case for non-operators.

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

**Status (main 6cd5f42).**
- done (#6): `created_by` for feedback is the literal `feedback`, and a feedback issue lands in `backlog`; only an `operator` can move it to `ready`.
- open: body size limit (`MaxBytesReader`) and per-field length limits on `/api/autopilot/feedback`.
- open: rate limit per IP and global (429).
- open: `AUTOPILOT_FEEDBACK_ORIGINS` allow-list and the shared-secret or signed-token mode.
- open: a separate untrusted field for the caller-supplied `user` (today it is not stored).
- open: the **untrusted** label and the operator confirmation before an agent can claim the issue (see 4.4).
- open: the off switch `AUTOPILOT_FEEDBACK=off` (the endpoint is always mounted).

**Tests status.** done (#6): the feedback creator is fixed and a feedback issue cannot reach `ready` without an operator. open: 413, 429 and disallowed-origin tests.

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

**Status (main 6cd5f42).**
- done (#6, #5): the actor is taken from the session. `created_by`, the comment `author` and `author_kind` are set by the server and the body values are ignored.
- open: the append-only `autopilot_audit` table, its use for create / edit / status / comment / upload / claim / runner / push / PR and for every denied privileged request.
- open: the audit trail on the board.

**Tests status.** done (#5, #6): spoofed `author`, `author_kind` and `created_by` are ignored. open: one audit row per transition and one denial row per refusal.

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

**Status (main 6cd5f42).**
- done (#6): self-heal never files straight to `ready`; `AUTOPILOT_SELF_HEAL_READY` is removed and self-heal bugs land in `backlog`. Self-heal itself stays opt-in (`AUTOPILOT_SELF_HEAL=1`).
- open: sanitise and truncate the request path and message text in self-heal issues (the path is stored as received).
- open: the isolated workspace as the default backend, and `AUTOPILOT_ALLOW_LOCAL_EXEC=1` for the in-place one. The `Env` / `Executor` abstraction exists (92754dd), but `local` is still the default.
- open: a scrubbed agent environment (the agent inherits the app's environment).
- open: an explicit tool policy. Today the runner uses `--permission-mode acceptEdits`; it never uses `bypassPermissions`.
- open: `trust = operator-confirmed`, so content from anonymous or low-role users is confirmed by an operator before the agent can claim it.
- open: the diff gate (protected paths, file and line limits).
- open: staging only paths that pass the gate (the runner still does `git add -A`).

**Tests status.** open: all of them (protected path, write outside the workdir, oversized output, child environment, injection fixture).

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

**Status (main 6cd5f42).**
- done (before #3): push and PR creation are off unless `AUTOPILOT_PUSH=1`, and branches are `autopilot/issue-<short id>`.
- open: the fixed repository and base-branch allow-list, and the refusal to start when `origin` does not match.
- open: refusing pushes outside `autopilot/*` and force pushes before git runs.
- open: branch names from the full issue id, and never deleting a branch the runner did not create (today a stale branch of the same name is deleted with `branch -D`).
- open: the scoped deploy key or fine-grained token guidance, and the clean-tree check that moves a dirty tree to `blocked`.

**Tests status.** open: mismatched remote, non-`autopilot/` push, force push.

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

**Status (main 6cd5f42).** open: the isolated-backend limits (non-root, CPU, memory, wall-clock), network egress default deny with an allow-list, an absolute and validated `AUTOPILOT_CLAUDE_BIN`, and the size limit on stored agent output (the runner trims the summary to 2000 characters; the `BLOCKED:` reason to 1500).

**Tests status.** open: egress to a non-listed host, truncation of oversized output.

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

**Status (main 6cd5f42).**
- done (before #3): the runner is off unless `AUTOPILOT_RUNNER=1`, separate from installing the plugin.
- done (#6): handing an issue to the agent (`ready`) is an `operator` action; a `reporter` cannot reach it.
- open: the audit row for the handoff (4.3).
- open: the best-effort branch-protection check at start.
- open: a test that no exported function or route merges or deploys (none is known to exist).

**Tests status.** done (#6): a `reporter` cannot reach `ready`. open: the handoff audit row and the no-merge-or-deploy test.

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

**Status (main 6cd5f42).**
- done (#7): the multipart attachment upload refuses a browser request whose `Origin` is not this host.
- done (#5): the board and the SDK escape quotes, sanitise class names and link only http(s) PR URLs.
- open: the redaction pass over agent output, comments, `result`, PR body and logs.
- open: warning on likely secrets in submitted text and refusing to hand such an issue to the agent.
- open: serving attachments with a fixed safe type, `X-Content-Type-Options: nosniff`, `Content-Disposition: attachment` outside an image / video allow-list, a sanitised filename and `Content-Security-Policy: sandbox`. Today the stored content type is served with `Content-Disposition: inline`.

**Tests status.** open: a `text/html` upload served as a download, and the planted-token redaction test.

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

**Status (main 6cd5f42).** done: the README states that Autopilot is single-tenant and every viewer sees every issue (decision 5 below is still open). open: `tenant_id` / `owner_id` columns and tenant-keyed WebSocket and attachment routes, only if the owner chooses to support multi-tenant.

**Tests status.** open: the two-tenant isolation test (not needed while multi-tenant stays unsupported).

### 4.10 Safe defaults

**Required.** Off unless explicitly enabled, least privilege, and **not in the
`create-togo-app` / `togo new` defaults** until this model is approved and the
tests in section 5 pass. Concretely:

| Setting | Default | Status (main 6cd5f42) |
|---|---|---|
| Plugin mounted | only when the app opts in | open: depends on the host app and on decision 1 |
| Auth plugin required | yes (fail closed) | done (#3) |
| Roles | no Autopilot role is granted implicitly | done (#6) |
| Feedback endpoint | off until `AUTOPILOT_FEEDBACK=on`, with limits | open: always mounted, no limits yet |
| Runner | off | done: needs `AUTOPILOT_RUNNER=1` |
| Execution backend | isolated workspace | open: `local` is the default |
| Push and PR creation | off | done: needs `AUTOPILOT_PUSH=1` |
| Self-heal | off, never straight to `ready` | done: needs `AUTOPILOT_SELF_HEAL=1`, files `backlog` (#6) |
| WebSocket origins | same origin only | done (#4); open sockets re-check the role (#7) |

## 5. Regression tests required for approval

Not only happy paths. The hardening release is blocked until these exist and pass
in CI (`go test ./...`, no network, SQLite in memory):

1. Unauthenticated caller against every route, including WebSocket and uploads. **done (#3, #6, #7)**
2. Signed-in user without an Autopilot role against every route. **done (#6)**
3. `reporter` and `viewer` attempting privileged transitions, `status=ready` on
   create, `human_only` changes, and claim reset. **done (#6)** except claim reset: **open**
4. Spoofed `author`, `author_kind`, `created_by` ignored. **done (#5, #6)**
5. Feedback abuse: oversized body, burst, disallowed origin. **open**
6. Stored-XSS payloads in every free-text field that reaches the browser.
   **done (#5)** for `kind`, `author_kind` and the quote escaping in the board and SDK; a pass over the remaining free-text fields is **open**
7. Cross-origin WebSocket handshake refused. **done (#4, #6, #7)**
8. Attachment with an active content type is not rendered inline. **open**
9. Runner: protected-path edit, out-of-workdir write, oversized diff, mismatched
   remote, push to a protected ref, dirty tree. All end in `blocked`. **open**
10. Agent environment contains none of the host's variables. **open**
11. Self-heal cannot create a `ready` issue and strips control text from paths.
    `ready` part **done (#6)**; stripping control text from paths **open**
12. Audit row exists for every transition and every denial. **open**

## 6. Decisions for the maintainer

All five are still **open**: none is approved by this change. Where the code already leans one way, it is noted.

1. **Default inclusion.** Recommendation: **do not include Autopilot in
   `create-togo-app` / `togo new` by default.** Offer it as an explicit opt-in
   (for example `togo install togo-framework/autopilot`, never implicit) that
   prints the safe-default table above. Revisit after the hardening release and
   the section 5 tests are green. *Nothing in `main` includes it by default; the
   decision is the owner's.*
2. **Feedback endpoint.** Keep it public with the controls in 4.2, or require a
   token for it. Recommendation: public with controls, off until enabled.
3. **Roles.** Own `viewer / reporter / operator` roles in the plugin, or map to the
   `auth` plugin's existing roles. Recommendation: map to `auth` roles if it has
   them, otherwise a minimal table owned by this plugin. *Code today (#6) maps to
   the `auth` plugin's roles `autopilot:viewer|reporter|operator`; the owner still
   confirms this choice.*
4. **Execution backend.** Make the isolated backend the default and require the
   explicit flag for in-place local runs. Recommendation: yes.
5. **Multi-tenant support.** Declare unsupported for now (4.9) or schedule the
   tenant columns. Recommendation: declare unsupported and say so in the README.
   *The README already says Autopilot is single-tenant; the decision is the owner's.*

## 7. Plan

Documentation first (this change), then small PRs, each with its bypass tests:
fail-closed auth and roles; actor from the session and the audit table; input
limits, feedback controls and the XSS and WebSocket fixes; the runner
(environment scrub, tool policy, diff gate, repository and branch limits); then
the release with notes. Any advisory is coordinated through private disclosure per
[SECURITY.md](../SECURITY.md).

Progress on main 6cd5f42: step 1 (fail-closed auth and roles) is done in #3 and #6 to #8, together with the
WebSocket and XSS fixes (#4, #5) and the upload origin check (#7). Not started: the audit table, input limits
and feedback controls, and the runner hardening.
