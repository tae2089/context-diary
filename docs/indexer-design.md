# Indexer Design v0.1

Status: Draft
Depends on: [Context Trailer Format v0.1](trailer-format.md)

## Goal

Turn git history into a queryable read model. `context-diary index` walks the
default branch, parses `Context-*` trailers, and upserts them into Postgres.
The database is disposable: git remains the source of truth, and dropping the
database plus re-running `index` rebuilds identical content.

### Change log

- v0.1: storage is **Postgres only**. An earlier draft default was embedded
  SQLite (zero-dependency local mode); dropped by project decision — one
  storage backend keeps the query layer (FTS, later pg_trgm/pgvector) and
  operational story singular. Cost: local use needs a Postgres instance
  (`docker run postgres` suffices).

## Modes

| Mode | Trigger | Status |
| --- | --- | --- |
| Local scan | `context-diary index` in a clone (manual / cron / CI step) | this doc |
| Webhook server | `context-diary serve` — push event → incremental index | later doc |

Both share the same ingestion core; `serve` is a loop around it.

## Configuration

- Database DSN: env `CONTEXT_DIARY_DB`, fallback `DATABASE_URL`. Never in
  TOML files — DSNs carry credentials.
- Repo identity: `--repo <name>` flag (default: directory name of the git
  top-level). Branch: `--branch` (default: current HEAD's branch).

## Schema

```sql
CREATE TABLE IF NOT EXISTS repos (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    cursor_hash   TEXT,                    -- last indexed commit (incremental)
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS commits (
    id            BIGSERIAL PRIMARY KEY,
    repo_id       BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    hash          TEXT   NOT NULL,
    subject       TEXT   NOT NULL,
    body          TEXT   NOT NULL,         -- full message, verbatim
    author_name   TEXT   NOT NULL DEFAULT '',
    author_email  TEXT   NOT NULL DEFAULT '',
    committed_at  TIMESTAMPTZ NOT NULL,
    context_why   TEXT   NOT NULL,         -- rows exist only for context entries
    search        tsvector GENERATED ALWAYS AS (
                    to_tsvector('simple', subject || ' ' || context_why || ' ' || body)
                  ) STORED,
    UNIQUE (repo_id, hash)
);
CREATE INDEX IF NOT EXISTS commits_committed_at_idx ON commits (repo_id, committed_at);
CREATE INDEX IF NOT EXISTS commits_search_idx ON commits USING GIN (search);

CREATE TABLE IF NOT EXISTS commit_scopes (
    commit_id     BIGINT NOT NULL REFERENCES commits(id) ON DELETE CASCADE,
    scope         TEXT   NOT NULL,
    PRIMARY KEY (commit_id, scope)
);
CREATE INDEX IF NOT EXISTS commit_scopes_scope_idx ON commit_scopes (scope);

CREATE TABLE IF NOT EXISTS commit_details (
    commit_id     BIGINT NOT NULL REFERENCES commits(id) ON DELETE CASCADE,
    kind          TEXT   NOT NULL CHECK (kind IN ('decision', 'ref')),
    position      INT    NOT NULL,
    value         TEXT   NOT NULL,
    PRIMARY KEY (commit_id, kind, position)
);
```

Notes:

- Only commits with a non-empty `Context-Why` get a row — the spec says
  trailer-less commits are "simply not indexed".
- `search` uses the `simple` dictionary (no language assumption; org content
  mixes languages). Query layer can add ranking later.
- `commit_details` folds decisions and refs into one ordered child table —
  same shape, same access pattern.
- DDL is applied idempotently at startup (`Migrate`). No migration framework
  until the first breaking change (YAGNI).

## Ingestion flow: `context-diary index`

```text
X1  resolve DSN from env                                     [env CONTEXT_DIARY_DB | DATABASE_URL]
X2    IF unset -> exit 1 "set CONTEXT_DIARY_DB"              (index is interactive; loud failure correct)
X3  open repo at git top-level                               [go-git PlainOpen]
X4    IF fails -> exit 1
X5  CALL connect Postgres + Migrate (idempotent DDL)
X6    IF fails -> exit 1
X7  upsert repos row by --repo name; read cursor_hash
X8  resolve branch head                                      [go-git ResolveRevision]
X9    IF fails -> exit 1
X10 walk first-parent from head back to cursor_hash (exclusive), collect
X11   IF cursor unreachable (history rewrite) -> full walk   (idempotent inserts make rescan safe; R3)
X12 reverse to oldest→newest
X13 FOR EACH commit:
X14   entry := index.EntryFromCommit(msg, meta)              (pure; nil when no Context-Why)
X15   IF entry == nil -> continue
X16   append to batch
X17 WRITE batch in one transaction:
X18   INSERT commits ON CONFLICT (repo_id, hash) DO NOTHING
X19   FOR EACH inserted commit: INSERT scopes + details
X20   UPDATE repos.cursor_hash = head
X21   IF tx fails -> rollback, exit 1                        (nothing partial; rerun safe)
X22 print "indexed N entries (M commits scanned)"
```

Completeness check (flow-design):

- Inputs at boundary: missing DSN X2, bad repo X4, bad branch X9.
- Side effects: DB connect/migrate X5-X6, transactional batch X17-X21 — each
  failure arm has an observable result (exit 1 + message). The single WRITE
  is atomic; cursor moves in the same transaction, so a crash never skips
  commits (worst case: re-scan already-indexed range, deduped by X18).
- Ordering: X18 before X19 (FK); X20 last inside tx.
- Concurrency: two concurrent `index` runs — X18's ON CONFLICT makes double
  insert harmless; last cursor write wins and both candidates are ≥ old
  cursor. Safe without locks.
- Rebuildability criterion: no state outside Postgres except git itself;
  X11 guarantees full rescan works from an empty or stale database.

## Query surface (for the MCP phase)

The schema is shaped for three axes; the MCP server composes them:

- **Scope timeline**: `WHERE scope = $1 ORDER BY committed_at` — "why does
  order cancellation work this way?"
- **Time window**: `WHERE committed_at BETWEEN ...` — "what changed here last
  quarter?"
- **Free text**: `WHERE search @@ websearch_to_tsquery('simple', $1)` —
  scope-less natural-language entry point.

## Limitations (recorded)

- **L1 First-parent by default.** Side-branch commits of a true merge are
  not indexed individually; squash-merge workflows are unaffected.
  Merge-commit teams lift this with `index --walk full`, which indexes the
  whole DAG (incremental via reachable-set difference from the cursor).
- **L2 No PR body ingestion.** Roadmap item for the webhook phase — PR
  descriptions live in the forge, not git.
- **L3 Function↔commit mapping is computed, not stored.** `context-diary
  explain` and the `explain_function` MCP tool compose `git log -L` with the
  index at query time — nothing extra is written at indexing time.

## Dependencies added

- `github.com/go-git/go-git/v5` — history walk (planned since language
  selection; indexer must run where git CLI may be absent).
- `github.com/jackc/pgx/v5` — Postgres driver (pgxpool).
