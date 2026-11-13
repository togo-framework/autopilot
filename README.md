# autopilot

The togo **autopilot** provider: an **Issues → Agent → Code → Deploy** loop built
into every togo app.

Humans (and agents) file issues; an in-app **runner** claims `ready` issues,
invokes **Claude Code** headless in the project working tree to implement them,
commits a branch / opens a PR, and moves the issue to review. When an agent
needs a human decision it moves the issue to **blocked** and comments — a human
reply flips it back to `ready` and the loop resumes. Multiple agents can share
one queue (claims are atomic). A **Feedback SDK** lets a "feedback button
everywhere" file issues from anywhere in your product.

Issues + comments live in the app DB (hybrid: an optional one-way GitHub mirror
via PRs). No external service required to run the board.

## Install

```bash
togo install togo-framework/autopilot
```

It registers a provider that mounts `/api/autopilot/*` and creates its tables
on boot. It quietly no-ops the auth guard if the `auth` plugin isn't installed.

## Workflow

```
backlog ─(human: hand off)→ ready ─(agent claims)→ in_progress
                                                        │
                        ┌───────────────────────────────┤
                        ▼                                ▼
   in_review ←(implemented: branch/PR)          blocked ─(human comment)→ ready
      │
   (human accepts)→ done
```

## API

| Method | Path | Purpose |
|---|---|---|
| `GET`  | `/api/autopilot/issues[?status=]` | list issues |
| `POST` | `/api/autopilot/issues` | create `{title, body, kind}`; an operator may also set `{assignee, status, priority, human_only}` |
| `GET`  | `/api/autopilot/issues/{id}` | issue + comments |
| `PATCH`| `/api/autopilot/issues/{id}` | edit title/body/kind/assignee/priority/human_only (a reporter: title/body of their own backlog issue only) |
| `POST` | `/api/autopilot/issues/{id}/status` | transition `{status}` (e.g. `ready`, `done`) |
| `GET`  | `/api/autopilot/issues/{id}/comments` | thread |
| `POST` | `/api/autopilot/issues/{id}/comments` | add `{body}` (author comes from the session) — an operator's comment on a **blocked** issue unblocks it |
| `POST` | `/api/autopilot/feedback` | Feedback SDK ingress (unauthenticated) `{message, page, user}` |

## The runner

Opt-in — set `AUTOPILOT_RUNNER=1`. It shells out to Claude Code + git, so it
runs where the CLI is authed and the repo is writable.

| Env | Default | Meaning |
|---|---|---|
| `AUTOPILOT_RUNNER` | _(off)_ | `1` starts the in-app runner |
| `AUTOPILOT_ALLOW_UNAUTHENTICATED` | _(off)_ | `1` mounts Autopilot without the `auth` plugin. Local development only; by default Autopilot does not mount without `auth`. Refused (nothing mounts) together with `AUTOPILOT_RUNNER=1` or when `APP_ENV`, `TOGO_ENV`, `GO_ENV` or `ENV` is `production`/`prod` |
| `AUTOPILOT_WS_ORIGINS` | _(same origin)_ | Extra origin hosts allowed to open the live event stream, comma separated |
| `AUTOPILOT_WORKDIR` | `.` | the git working tree the agent edits |
| `AUTOPILOT_CLAUDE_BIN` | `claude` | Claude Code binary |
| `AUTOPILOT_POLL_SECONDS` | `15` | queue poll interval |
| `AUTOPILOT_PUSH` | _(off)_ | `1` pushes the branch + opens a PR via `gh` |
| `AUTOPILOT_AGENT_ID` | `togo-agent` | identity that claims/comments (unique per agent for multi-agent) |
| `AUTOPILOT_COMMIT_NAME` / `_EMAIL` | `togo agent` / `agent@togo.dev` | commit author |

The agent is instructed to either edit files, or — if it can't proceed without
a human decision — make no changes and reply `BLOCKED: <question>`, which routes
the issue to `blocked`.

## Real-time (WebSocket)

`GET /api/autopilot/ws` streams live events so the board + launcher update
instantly (no polling): `issue.created`, `issue.status` (moves, incl. an agent
claiming → `in_progress`), `issue.updated`, `comment.added`. Self-contained (own
WS hub — no dependency on the realtime plugin). Behind a dev proxy, enable WS
upgrade for `/api` (`ws: true`).

## Board (Mission Control)

A self-contained board ships with the plugin — no build step. Open **`/autopilot`**
in the browser: a six-column workflow (backlog → done), create issues, open an
issue to read/append the human↔agent thread, hand issues to agents, and accept or
re-run reviews. It reads/writes `/api/autopilot/*` (auth-guarded when `auth` is
installed).

## Feedback SDK

Drop a "feedback button everywhere" with one line — no bundler:

```html
<script src="/autopilot/feedback.js" data-user="me@co"></script>
```

It injects a floating button; submissions `POST /api/autopilot/feedback` and land
as `feedback` issues on the board. It also exposes `window.AutopilotFeedback`
(`open`, `close`, `submit`) so a framework component can drive it.

## Autopilot SDK — launcher + push notifications

`<script src="/autopilot/sdk.js" defer></script>` adds a floating launcher (opens
the board as an overlay) **and a built-in notification system**: it subscribes to
the WebSocket and surfaces every event as a **clickable toast** — new issue,
status move, `agent started`, **blocked (needs your input)**, ready for review,
new comment — each typed/colored by severity, with an **unread pip** on the
launcher. Clicking a toast opens the board **deep-linked to that issue**
(`/autopilot?issue=<id>`) so you can follow up in one click. `window.Autopilot`
exposes `open`/`openTo`/`close`/`toggle`/`notify`; `data-toasts="off"` disables
the toasts.

## Roles

Autopilot has no user table of its own. It uses the roles of the `auth` plugin,
with these fixed names (matched exactly; there is no setting to change them, and
the host app's own `admin`/`viewer` roles grant nothing here):

| Role | Can |
|---|---|
| `autopilot:viewer` | read issues, comments, attachments and the live stream |
| `autopilot:reporter` | viewer + create issues (always created as `backlog`), comment on and reword (title/body, while still `backlog`) the issues they created |
| `autopilot:operator` | reporter + hand work to the agent (`ready`), change status, assign, set priority/`human_only`, edit and comment on any issue, upload attachments |

A signed-in user with none of these gets `403` on every data route and the live
stream; no session is `401`. The board page itself (`/autopilot`) is only a shell:
all data calls behind it are guarded. The public feedback ingress
(`/api/autopilot/feedback`) stays open and always files a `backlog` issue.

> **`autopilot:operator` is code execution on the runner host when
> `AUTOPILOT_RUNNER=1`**: an operator decides which issues the agent works on,
> and the issue text is its prompt. Grant it like root, to few people.

**Breaking change:** installs that have the `auth` plugin but no Autopilot roles
get `403` until a role is granted. Nothing is granted automatically. Roles are a
comma-separated list in `users.roles`:

```sql
UPDATE users SET roles = roles || ',autopilot:operator' WHERE email = 'you@example.com';
-- or, if roles is empty: SET roles = 'autopilot:operator'
```

Things to know:

- Roles are baked into the session token when it is issued. Reads (GET) use
  those token roles, so a revoked role can still read until the token expires
  (`AUTH_TTL_HOURS`, 24 h by default). Every write (POST/PATCH: create, edit,
  status, comment, upload) re-reads the account's current roles with the auth
  plugin's `UserByID`, so a demoted operator gets `403` and a deleted account
  `401` on the very next write. This costs one account lookup per write.
- An open live-stream (WebSocket) connection re-checks its account every 30
  seconds against the auth plugin's `UserByID` (current roles) and re-validates
  the session. If the role was revoked, the account deleted or the session
  expired, the stream is closed (policy violation) and a reconnect is refused.
  The `Origin` is checked when the connection opens.
- Attachment uploads are `multipart/form-data`, so they are not covered by the
  JSON-only rule; instead a browser request whose `Origin` is not this host
  (including a sibling subdomain or `null`) is refused with `403`. Requests with
  no `Origin` (curl, server-side clients) are not affected.
- Personal access tokens carry abilities, not roles, so a PAT is refused (`403`).
- The mutating routes accept `application/json` bodies only (`415` otherwise).
- Self-healing (`AUTOPILOT_SELF_HEAL=1`) files bugs as `backlog` only; an
  operator decides when the agent starts.
- Autopilot is single-tenant: every viewer sees every issue.

## Security

The runner executes Claude Code with file-edit permission and can push branches,
so it is **opt-in** (`AUTOPILOT_RUNNER=1`) and meant to run where the repo and
CLI credentials already live. See [SECURITY.md](SECURITY.md).

## Status

Core loop is proven end-to-end (issue → real Claude Code implements → branch →
review), unit-tested (state machine, atomic claim, unblock), with the board +
Feedback SDK shipping in-plugin. GitHub PR mirror is wired on push; multi-agent
dashboards, the project-startup prompt, and `create-togo-app` bundling are next.
