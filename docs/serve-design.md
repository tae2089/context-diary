# Serve Design v0.2 — GitHub PR Bot + MCP Endpoint

Status: Draft (v0.2: async ingest queue + commit statuses)
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
│                         tools: search_context, list_scopes,
│                                explain_function, related_by_ref
├─ /ui/                   read-only web UI (server-rendered, no JS):
│                         search (FTS+trigram) + scope/repo filters
└─ /healthz
```

## Configuration (env only — all are secrets or deployment-specific)

| Env | Purpose |
| --- | --- |
| `CONTEXT_DIARY_DB` (or `DATABASE_URL`) | Postgres DSN |
| `GITHUB_WEBHOOK_SECRET` | HMAC-SHA256 webhook verification |
| `CONTEXT_DIARY_MCP_TOKEN` | optional bearer token guarding `/mcp` |
| `CONTEXT_DIARY_BASE_URL` | optional public URL of this server; when set, status Details links open the server's own `/checks/{id}` pages (Atlantis-style) instead of the bot comment |
| **GitHub auth — one of:** | |
| `GITHUB_TOKEN` | PAT (comments, statuses, mirror clone) — wins when set |
| `GITHUB_APP_ID` + `GITHUB_APP_INSTALLATION_ID` + `GITHUB_APP_PRIVATE_KEY` (or `_FILE`) | GitHub App: RS256 app JWT → installation token, cached and auto-refreshed hourly. Preferred for real deployments (per-repo installation scope, rotating tokens). |

Flags: `--addr :8080`, `--cache-dir` (bare mirrors; default user cache dir),
`--walk first-parent|full` (same semantics as `index`).

## Flow: PR opened / edited / reopened / synchronize

```text
W1  verify X-Hub-Signature-256 (HMAC, constant-time)
W2    IF invalid/missing -> 401, no side effects
W3  parse event; IF not pull_request or unhandled action -> 200 "ignored"
W4  evaluate BOTH context carriers (preview.Evaluate):
      body path   — PR description trailers (squash teams)
      commit path — every non-merge branch commit has trailers
                    (merge/rebase teams; GET /pulls/{n}/commits, first
                    100; fetch failure degrades to body-only)
      pass = either path
W5  render ONE comment:
      body path ✅   -> "will be indexed as" preview (why/scopes/decisions)
      commit path ✅ -> per-commit why list + squash-discard warning
      both ❌        -> body violations + offending commits + template
W6  CALL GitHub: find bot comment by marker in first page of comments
W7    found -> PATCH comment; not found -> POST comment
W8    IF GitHub call fails -> 502 logged (redelivery is MANUAL on GitHub;
      the next PR event retries naturally)
W8a CALL set commit status on head SHA: context-diary/context
      success "context trailers present" | failure "missing trailers"
      target_url = /checks/{id} on this server when CONTEXT_DIARY_BASE_URL
      is set (Atlantis-style detail page, in-memory, restart-ephemeral),
      else the bot comment's html_url
      -> branch protection can REQUIRE this status, blocking merge harder
         than the comment can
W8b   IF status call fails -> log only (comment is the primary UX)
W9  200
```

The bot maintains exactly one comment per PR (marker `<!-- context-diary -->`),
updated in place — no comment spam across pushes.

## Flow: PR closed (merged == true)

```text
M1  verify signature (as W1-W2)
M2  parse; IF merged != true -> 200 "ignored"
M3  enqueue {repo, event} on the ingest queue (bounded, in-memory)
M4    IF queue full -> 503 (operator signal; no partial side effects)
M5  set commit status on merge SHA: context-diary/ingest = pending
M6  202 "ingest queued"                      (webhook never waits on git/db)

worker (per-repo serialized, cross-repo parallel):
M7  sync bare mirror: clone once into cache-dir, else fetch (token auth)
M8  ingest.Run(store, mirror, repoName, default branch, walk mode)
      — identical path to `context-diary index`: cursor, ON CONFLICT dedup
M9  set status on merge SHA:
      success "indexed N entries" | error <reason>
      target_url = the same /checks/{id} page as the pending status,
      updated in place with the result + warnings (when base URL set)
      (terminal status always set on error paths so pending never dangles)
```

### Async queue semantics (v0.2)

Atlantis-shaped: ACK immediately, work in background goroutines, in-memory
only. A restart drops queued jobs — accepted because the cursor makes the
next merge (or a manual `context-diary index`) catch up losslessly; the
worst case is a `pending` status that never resolves on that one commit.
GitHub does NOT auto-redeliver failed webhooks (manual redelivery only), so
the 10s webhook timeout is the hard reason to queue: the first delivery for
a repo pays a full clone, which can exceed it inline. Capacity 256, 4
workers, per-repo mutex (same-repo jobs serialize; the cursor makes
concurrent same-repo ingests wasteful, not wrong).

## MCP endpoint

Official `github.com/modelcontextprotocol/go-sdk`, streamable HTTP at `/mcp`.

| Tool | Args | Returns |
| --- | --- | --- |
| `search_context` | `repo?`, `scope?`, `query?` (websearch text), `since?`/`until?` (RFC3339), `limit?` | entries: hash, subject, why, scopes, decisions, refs, author, committed_at |
| `list_scopes` | `repo?` | distinct scope slugs with entry counts |
| `explain_function` | `repo`, `file`, `function`, `branch?` | why-timeline of one function: `git log -L` commits joined with index entries; `has_context=false` marks backfill candidates; `referenced_by` lists entries in OTHER repos whose code refs point at this function. Requires the repo's mirror and the git CLI on the server. |
| `related_by_ref` | `ref` | entries across ALL repos whose Context-Ref values contain the text (Jira key, doc URL, code ref) — "which repos did this ticket/incident touch". |

Both are read-only over the store; `repo` omitted = across all indexed
repos. Answer-language/audience translation is the calling assistant's job
(write-once-developer-level principle).

## Web UI

`/ui/` is a server-rendered read view over the index: a search box
(FTS + trigram, so Korean stems match), scope chips with counts, a repo
filter, and entry cards (why, decisions, refs, forge commit links).
html/template embedded in the binary — no JavaScript, no frontend
toolchain, nothing to build or deploy separately. Same trust posture as
/mcp's default: unauthenticated, deploy inside the network boundary
(auth middleware is a later phase; bearer tokens do not fit browsers).

## Security notes

- Webhook body is untrusted until W1 passes; parse only after verification.
- `GITHUB_TOKEN` scope: `repo` (comments + clone). Never logged; mirror
  remotes embed it only in-memory (cache dir stores plain bare repos,
  fetch URL is reconstructed per request).
- MCP endpoint auth: set `CONTEXT_DIARY_MCP_TOKEN` and clients must send
  `Authorization: Bearer <token>` (constant-time compare). Unset = open,
  logged as a startup warning — acceptable only inside a trusted network.
  GitHub App auth (replacing the PAT, unlocking Checks API and short-lived
  tokens) is the planned next step.

## Out of scope (this phase)

- Slash commands in PR comments (`/context suggest` …)
- Server-side LLM drafting (re-open the provider-adapter discussion first)
- GitLab/Bitbucket adapters (forge boundary kept: signature+parse+comment
  isolated in internal/forge/github)
- MCP auth, PR-body forge-API ingestion (L2)
