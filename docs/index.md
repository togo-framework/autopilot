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
| `POST` | `/api/autopilot/issues` | create `{title, body, kind, assignee, status}` |
| `GET`  | `/api/autopilot/issues/{id}` | issue + comments |
| `PATCH`| `/api/autopilot/issues/{id}` | edit title/body/kind/assignee |
| `POST` | `/api/autopilot/issues/{id}/status` | transition `{status}` (e.g. `ready`, `done`) |
| `GET`  | `/api/autopilot/issues/{id}/comments` | thread |
| `POST` | `/api/autopilot/issues/{id}/comments` | add `{author, author_kind, body}` — human comment on a **blocked** issue unblocks it |
| `POST` | `/api/autopilot/feedback` | Feedback SDK ingress (unauthenticated) `{message, page, user}` |

## The runner

Opt-in — set `AUTOPILOT_RUNNER=1`. It shells out to Claude Code + git, so it
runs where the CLI is authed and the repo is writable.

| Env | Default | Meaning |
|---|---|---|
| `AUTOPILOT_RUNNER` | _(off)_ | `1` starts the in-app runner |
| `AUTOPILOT_WORKDIR` | `.` | the git working tree the agent edits |
| `AUTOPILOT_CLAUDE_BIN` | `claude` | Claude Code binary |
| `AUTOPILOT_POLL_SECONDS` | `15` | queue poll interval |
| `AUTOPILOT_PUSH` | _(off)_ | `1` pushes the branch + opens a PR via `gh` |
| `AUTOPILOT_AGENT_ID` | `togo-agent` | identity that claims/comments (unique per agent for multi-agent) |
| `AUTOPILOT_COMMIT_NAME` / `_EMAIL` | `togo agent` / `agent@togo.dev` | commit author |

The agent is instructed to either edit files, or — if it can't proceed without
a human decision — make no changes and reply `BLOCKED: <question>`, which routes
the issue to `blocked`.

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

## Security

The runner executes Claude Code with file-edit permission and can push branches,
so it is **opt-in** (`AUTOPILOT_RUNNER=1`) and meant to run where the repo and
CLI credentials already live. See [SECURITY.md](SECURITY.md).

## Status

Core loop is proven end-to-end (issue → real Claude Code implements → branch →
review), unit-tested (state machine, atomic claim, unblock), with the board +
Feedback SDK shipping in-plugin. GitHub PR mirror is wired on push; multi-agent
dashboards, the project-startup prompt, and `create-togo-app` bundling are next.
