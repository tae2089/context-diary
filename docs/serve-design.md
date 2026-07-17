# Serve Design v0.1 — GitHub PR Bot + MCP Endpoint

Status: Draft
Depends on: [Indexer Design v0.1](indexer-design.md), [Trailer Format v0.1](trailer-format.md)

## Goal

One deployment that makes context-diary org-wide without per-developer
setup: a GitHub webhook bot that reviews PR descriptions the way Atlantis
reviews Terraform plans, plus an MCP endpoint so anyone's AI assistant can
query the index.

```
context-diary serve
├─ POST /webhook/github   PR opened/edited → lint body, upsert preview comment
│                         PR merged        → fetch mirror, ingest (same path as CLI)
├─ /mcp                   MCP streamable HTTP (official Go SDK)
│                         tools: search_context, list_scopes
└─ /healthz
```

## Configuration (env only — all are secrets or deployment-specific)

| Env | Purpose |
| --- | --- |
| `CONTEXT_DIARY_DB` (or `DATABASE_URL`) | Postgres DSN |
| `GITHUB_TOKEN` | PR comments + mirror clone auth |
| `GITHUB_WEBHOOK_SECRET` | HMAC-SHA256 webhook verification |

Flags: `--addr :8080`, `--cache-dir` (bare mirrors; default user cache dir),
`--walk first-parent|full` (same semantics as `index`).

## Flow: PR opened / edited / reopened / synchronize

```text
W1  verify X-Hub-Signature-256 (HMAC, constant-time)
W2    IF invalid/missing -> 401, no side effects
W3  parse event; IF not pull_request or unhandled action -> 200 "ignored"
W4  lint PR body (lint-message semantics: synthetic subject + body)
W5  render ONE comment:
      clean  -> "will be indexed as" preview (why/scopes/decisions)
      dirty  -> violations + copy-paste template block
W6  CALL GitHub: find bot comment by marker in first page of comments
W7    found -> PATCH comment; not found -> POST comment
W8    IF GitHub call fails -> 502 logged; GitHub redelivers on retry
W9  200
```

The bot maintains exactly one comment per PR (marker `<!-- context-diary -->`),
updated in place — no comment spam across pushes.

## Flow: PR closed (merged == true)

```text
M1  verify signature (as W1-W2)
M2  parse; IF merged != true -> 200 "ignored"
M3  sync bare mirror: clone once into cache-dir, else fetch (token auth)
M4    IF sync fails -> 500 logged (redelivery retries)
M5  ingest.Run(store, mirror, repoName, default branch, walk mode)
      — identical path to `context-diary index`: cursor, ON CONFLICT dedup
M6    IF ingest fails -> 500 logged
M7  200 with inserted count
```

Inline (no queue): GitHub webhook timeout is 10s; incremental fetch+index is
sub-second in steady state. First delivery per repo pays the full clone —
acceptable MVP cost, revisit with a queue if a target repo proves too large.

## MCP endpoint

Official `github.com/modelcontextprotocol/go-sdk`, streamable HTTP at `/mcp`.

| Tool | Args | Returns |
| --- | --- | --- |
| `search_context` | `repo?`, `scope?`, `query?` (websearch text), `since?`/`until?` (RFC3339), `limit?` | entries: hash, subject, why, scopes, decisions, refs, author, committed_at |
| `list_scopes` | `repo?` | distinct scope slugs with entry counts |

Both are read-only over the store; `repo` omitted = across all indexed
repos. Answer-language/audience translation is the calling assistant's job
(write-once-developer-level principle).

## Security notes

- Webhook body is untrusted until W1 passes; parse only after verification.
- `GITHUB_TOKEN` scope: `repo` (comments + clone). Never logged; mirror
  remotes embed it only in-memory (cache dir stores plain bare repos,
  fetch URL is reconstructed per request).
- MCP endpoint is unauthenticated in MVP — deploy inside the network
  boundary (self-hosted, internal staff), same posture as the CLI design.
  Auth middleware slot is a later phase.

## Out of scope (this phase)

- Slash commands in PR comments (`/context suggest` …)
- Server-side LLM drafting (re-open the provider-adapter discussion first)
- GitLab/Bitbucket adapters (forge boundary kept: signature+parse+comment
  isolated in internal/forge/github)
- MCP auth, PR-body forge-API ingestion (L2)
