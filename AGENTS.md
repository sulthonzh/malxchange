# AGENTS.md

Instructions for coding agents working in this repository.

## Agent skills

### Issue tracker

Issues are tracked as GitHub issues via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Five canonical triage roles using the default label strings (e.g. `ready-for-agent`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.

### Review checklist

Rules the code reviewer enforces on every diff. See `docs/agents/review-checklist.md`.

### Worktrees

Sibling worktrees under `../spec24-wt/` hold in-flight ticket branches; the primary checkout is merge-only. Editing code: address files by absolute path and confirm the first edit of a session with `git status` in the intended worktree — a relative path can silently land in the primary checkout.
