# context-diary

[![CI](https://github.com/tae2089/context-diary/actions/workflows/ci.yml/badge.svg)](https://github.com/tae2089/context-diary/actions/workflows/ci.yml)

Capture the **why** behind every code change — and let anyone in your team ask
about it in natural language.

`git log` tells you *what* changed. Code review tells you *whether* it should
change. Six months later, nobody remembers *why* it changed. context-diary
records that context at commit time and makes it queryable — including by
people who will never run `git log`.

## How it works

Git is the source of truth. The server is a read-only index.

1. **Convention + hook** — commits carry structured context in
   [git trailers](docs/trailer-format.md) (`Context-Why`, `Context-Scope`, …).
   AI coding agents (Claude Code, Codex, …) write them from a convention
   snippet; a lint hook enforces them and feeds violations back so agents
   self-correct. Humans get a template in the commit editor. No API calls —
   everything runs locally.
2. **Indexer** — `context-diary index` parses default-branch commit trailers
   into Postgres (run it locally, from cron, or in CI; a webhook server mode
   is planned). The database is a disposable read model — drop it and
   re-index to rebuild everything from git.
3. **Server** (`context-diary serve`) — one deployment for the whole org:
   - a GitHub PR bot that reviews PR descriptions Atlantis-style (one
     bot comment: index preview when clean, template when not), sets a
     `context-diary/context` commit status you can require via branch
     protection, and indexes merges asynchronously with a
     `context-diary/ingest` status (pending → success) on the merge commit;
   - an MCP endpoint (`/mcp`) exposing `search_context` / `list_scopes`, so
     anyone — including non-developers — can ask "why does order cancellation
     work this way?" from their AI assistant and get an answer translated to
     their level.

## Status

Early. The [trailer format spec](docs/trailer-format.md) and the
`context-diary` CLI (hooks, lint, agent setup) are usable; the indexer and
MCP server are not built yet.

```sh
go install github.com/tae2089/context-diary/cmd/context-diary@latest
cd your-repo
context-diary init --agent claude-code   # hooks + config + CLAUDE.md snippet
```

Roadmap:

- [x] Trailer format spec (v0.1)
- [x] Commit hook / CLI (`context-diary` binary)
- [x] Indexer (`context-diary index` → Postgres)
- [x] Server: GitHub PR bot + MCP endpoint (`context-diary serve`)
- [x] GitHub App auth (PAT remains supported; App preferred for deployments)
- [x] Backfill: context for pre-adoption history via [git notes](docs/backfill.md)
- [ ] Web UI (if demand proves out)

`serve` is deliberately single-instance (in-memory queue, local mirror
cache) — the right trade for a self-hosted OSS deployment. Set
`CONTEXT_DIARY_MCP_TOKEN` to require a bearer token on `/mcp`.

## Merge strategies

Trailers must survive the path to your default branch.
**Recommendation: don't use merge commits.** Use rebase merge or squash
merge, and disable the rest in the repository settings so the guideline is
enforced, not just documented:

> Settings → General → Pull Requests: uncheck **"Allow merge commits"**;
> for squash, set the default message to **"Pull request title and
> description"**.

| Merge strategy | What to do |
| --- | --- |
| Rebase merge (recommended) | Nothing — commits land unchanged. |
| Squash merge (recommended) | Write trailers in the PR description; with the setting above they become the commit message. Enforce before merge with ONE of: the `context-diary/context` status from a running `serve` (its Details link opens the bot comment), or the [PR lint action](examples/github-actions/pr-context-lint.yml) for teams without a server — running both is redundant. |
| Merge commits (discouraged) | Trailers never reach the merge commit itself and side-branch commits are off the default first-parent walk. If you must keep them (or for pre-adoption history), index with `context-diary index --walk full`. |

Either way, `context-diary lint` in CI on the default branch is the safety
net that catches anything that slipped through.

## Design principles

- **Git is the source of truth.** The server can be rebuilt from history at
  any time; losing it loses nothing.
- **Write once, at developer level.** Audience translation (e.g. for PMs or
  CS) happens at query time via AI, not at write time.
- **Self-hosted.** One binary for hooks, indexing, and (soon) serving.
  Storage is Postgres; the index is disposable and rebuildable, so it needs
  no backups.

## License

[MIT](LICENSE)
