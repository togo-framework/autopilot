# Security policy — togo autopilot

Autopilot runs an autonomous agent that **edits code and can push branches**, so
its threat model is different from a normal plugin. The API/board are safe to run
anywhere; the **runner** is the privileged part and is off by default.

## Trust boundaries

- **API + board** (`/api/autopilot/*`, `/autopilot`): auth-guarded when the `auth`
  plugin is installed (session middleware). Only the **Feedback** ingress
  (`POST /api/autopilot/feedback`) is intentionally unauthenticated so a public
  "feedback button" can file issues — it can only create `feedback` issues in
  `backlog`, never move an issue or run the agent.
- **Runner** (`AUTOPILOT_RUNNER=1`): opt-in. It shells out to Claude Code and
  `git`, so it must run only where you already trust the repo checkout and the CLI
  credentials (a build box or a developer machine) — never expose it to untrusted
  input paths.

## Hardening in place

- **Runner is opt-in.** With `AUTOPILOT_RUNNER` unset the agent never runs; the
  board is a plain issue tracker.
- **A human gates every merge.** The agent produces a branch and moves the issue
  to `in_review`; it does not merge or deploy. Accepting is a human action.
- **Blocked-by-default on ambiguity.** The agent is instructed to make **no
  changes** and reply `BLOCKED:` when a human decision is needed, rather than guess.
- **Scoped edits.** Claude Code runs with `--permission-mode acceptEdits` inside
  `AUTOPILOT_WORKDIR` only; it is told not to run git itself (the runner owns git).
- **Atomic claim.** Issue claiming is a single conditional `UPDATE`, so multiple
  agents on one queue never double-implement an issue.
- **No secrets in issues.** Treat issue/comment bodies as untrusted prompt input;
  do not paste secrets into them (they are handed to the agent verbatim).

## Recommended production posture

- Run the runner on an isolated host/container with a scoped deploy key.
- Keep `AUTOPILOT_PUSH` off unless the box has a least-privilege `gh`/git token.
- Review every agent PR before merge; use branch protection on the default branch.

## Reporting

Report vulnerabilities via a private security advisory on the repo, or to the
togo-framework maintainers. Please do not open public issues for security reports.
