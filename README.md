# context-diary

**English** | [한국어](README.ko.md)

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
   - an MCP endpoint (`/mcp`) exposing `search_context` / `list_scopes` /
     `explain_function` / `related_by_ref`, so anyone — including
     non-developers — can ask "why does order cancellation work this way?"
     from their AI assistant and get an answer translated to their level;
   - a read-only web UI (`/ui/`) for browsing and searching the index
     without any AI assistant at all.

## Status

Early. The [trailer format spec](docs/trailer-format.md) and the
`context-diary` CLI (hooks, lint, agent setup) are usable; the indexer and
MCP server are not built yet.

```sh
go install github.com/tae2089/context-diary/cmd/context-diary@latest
cd your-repo
context-diary init --agent claude-code   # hooks + config + CLAUDE.md snippet
```

Or run the server from the Docker image (needs git-capable runtime — included):

```sh
docker build -t context-diary .
docker run -p 8080:8080   -e CONTEXT_DIARY_DB=postgres://...   -e GITHUB_WEBHOOK_SECRET=...   -e GITHUB_APP_ID=... -e GITHUB_APP_INSTALLATION_ID=...   -v ctxdiary-keys:/keys -e GITHUB_APP_PRIVATE_KEY_FILE=/keys/app.pem   -v ctxdiary-mirrors:/var/cache/context-diary   context-diary   # default command: serve
```

Roadmap:

- [x] Trailer format spec (v0.1)
- [x] Commit hook / CLI (`context-diary` binary)
- [x] Indexer (`context-diary index` → Postgres)
- [x] Server: GitHub PR bot + MCP endpoint (`context-diary serve`)
- [x] GitHub App auth (PAT remains supported; App preferred for deployments)
- [x] Backfill: context for pre-adoption history via [git notes](docs/backfill.md)
- [x] Web UI (`/ui/` on serve — search, scope browse; read-only, no JS)

`serve` is deliberately single-instance (in-memory queue, local mirror
cache) — the right trade for a self-hosted OSS deployment. Set
`CONTEXT_DIARY_MCP_TOKEN` to require a bearer token on `/mcp`.

## Merge strategies

Trailers must survive the path to your default branch. The PR bot validates
BOTH carriers and passes when either does: trailers in the PR description
(squash teams), or trailers on every non-merge branch commit (merge/rebase
teams). Pick the row that matches how much context you want to keep:

| Merge strategy | Context granularity | What to do |
| --- | --- | --- |
| Merge commits (context-maximal — best for AI-agent-authored repos) | every branch commit, individually | Commits carry trailers (the hook + agents handle this); index and serve with `--walk full`. The merge commit itself carries none — it is a stitch, not a change. |
| Squash merge (least discipline — best for human-heavy teams) | one entry per PR | Write trailers in the PR description and set the squash default to **"Pull request title and description"**. Branch WIP commits need nothing — they are discarded. |
| Rebase merge | every branch commit | Commits land unchanged. Needs the same per-commit discipline as merge commits, plus clean-history habits (no WIP commits). |

Enforce before merge with the `context-diary/context` required status from a
running `serve`, or the [PR lint action](examples/github-actions/pr-context-lint.yml)
(PR-description path only) for teams without a server. Either way,
`context-diary lint` in CI on the default branch is the safety net that
catches anything that slipped through.

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
