# @togo-framework/agent-loop

**Issues → Agent → Code → Deploy**, built into every togo app.

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
togo install togo-framework/agent-loop
```

It registers a provider that mounts `/api/agent-loop/*` and creates its tables
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
| `GET`  | `/api/agent-loop/issues[?status=]` | list issues |
| `POST` | `/api/agent-loop/issues` | create `{title, body, kind, assignee, status}` |
| `GET`  | `/api/agent-loop/issues/{id}` | issue + comments |
| `PATCH`| `/api/agent-loop/issues/{id}` | edit title/body/kind/assignee |
| `POST` | `/api/agent-loop/issues/{id}/status` | transition `{status}` (e.g. `ready`, `done`) |
| `GET`  | `/api/agent-loop/issues/{id}/comments` | thread |
| `POST` | `/api/agent-loop/issues/{id}/comments` | add `{author, author_kind, body}` — human comment on a **blocked** issue unblocks it |
| `POST` | `/api/agent-loop/feedback` | Feedback SDK ingress (unauthenticated) `{message, page, user}` |

## The runner

Opt-in — set `AGENTLOOP_RUNNER=1`. It shells out to Claude Code + git, so it
runs where the CLI is authed and the repo is writable.

| Env | Default | Meaning |
|---|---|---|
| `AGENTLOOP_RUNNER` | _(off)_ | `1` starts the in-app runner |
| `AGENTLOOP_WORKDIR` | `.` | the git working tree the agent edits |
| `AGENTLOOP_CLAUDE_BIN` | `claude` | Claude Code binary |
| `AGENTLOOP_POLL_SECONDS` | `15` | queue poll interval |
| `AGENTLOOP_PUSH` | _(off)_ | `1` pushes the branch + opens a PR via `gh` |
| `AGENTLOOP_AGENT_ID` | `togo-agent` | identity that claims/comments (unique per agent for multi-agent) |
| `AGENTLOOP_COMMIT_NAME` / `_EMAIL` | `togo agent` / `agent@togo.dev` | commit author |

The agent is instructed to either edit files, or — if it can't proceed without
a human decision — make no changes and reply `BLOCKED: <question>`, which routes
the issue to `blocked`.

## Status

Core loop is proven end-to-end (issue → real Claude Code implements → branch →
review) and unit-tested (state machine, atomic claim, unblock). Web board UI,
Feedback SDK client package, GitHub mirror, and multi-agent dashboards are in
progress.
