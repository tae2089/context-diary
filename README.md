# context-diary

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
   An AI-assisted hook helps write them.
2. **Indexer** — on merge to the default branch, a webhook parses commit
   trailers, bodies, and PR descriptions into a local SQLite index.
3. **MCP server** — exposes the index to AI assistants, so anyone — including
   non-developers — can ask "why does order cancellation work this way?" and
   get an answer translated to their level.

## Status

Early design. The [trailer format spec](docs/trailer-format.md) is the first
stable artifact. Nothing is runnable yet.

Roadmap:

- [x] Trailer format spec (v0.1)
- [ ] Commit hook / CLI (`context-diary` binary)
- [ ] Indexer (webhook + SQLite)
- [ ] MCP server
- [ ] Web UI (if demand proves out)

## Design principles

- **Git is the source of truth.** The server can be rebuilt from history at
  any time; losing it loses nothing.
- **Write once, at developer level.** Audience translation (e.g. for PMs or
  CS) happens at query time via AI, not at write time.
- **Self-hosted, adapter-based.** Generic core; git host, LLM provider, and
  storage are pluggable. Default storage is pure-Go SQLite — one binary, no
  external dependencies.

## License

[MIT](LICENSE)
