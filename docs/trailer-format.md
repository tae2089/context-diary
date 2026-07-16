# Context Trailer Format v0.1

Status: Draft
License: MIT

## Purpose

context-diary captures the **why** behind a code change at the moment it is
made, using [git trailers](https://git-scm.com/docs/git-interpret-trailers) in
the commit message. Git remains the single source of truth; the indexer and
MCP server only read what this format defines.

This document is the contract between:

- **writers** — developers (or AI commit hooks) composing commit messages,
- **readers** — the indexer parsing merged commits on the default branch.

## Placement

Trailers MUST appear in the last paragraph of the commit message, per git's
trailer rules. Recommended commit layout:

```
<subject line>

<body: free-form explanation, as long as needed>

<trailer block>
```

The body carries long-form narrative. Trailers carry the structured,
machine-queryable summary. The indexer stores both.

## Grammar

- Key: `Context-` prefix followed by a registered name. Keys are
  case-insensitive on read; writers SHOULD use the canonical casing below.
- Separator: `: ` (colon, space).
- Value: a single line of UTF-8 text. Multiline values are **forbidden** —
  continuation-line support is inconsistent across git tooling. If one line is
  not enough, put the detail in the body and keep the trailer as a summary.
- Unknown `Context-*` keys MUST be ignored by the reader (forward
  compatibility), and MUST NOT cause a parse failure.

## Key registry

| Key                | Required | Repeatable | Value                                                                 |
| ------------------ | -------- | ---------- | --------------------------------------------------------------------- |
| `Context-Why`      | yes      | no         | One-line reason this change exists. Written for a developer audience. |
| `Context-Scope`    | no       | yes        | Feature/domain slug the change belongs to. See slug grammar below.    |
| `Context-Decision` | no       | yes        | A decision made, ideally `chosen over rejected; reason` shaped.       |
| `Context-Ref`      | no       | yes        | URL or identifier of related material (issue, ADR, incident, doc).    |

`Context-Why` is the only required key. A commit with no `Context-Why` trailer
is simply not indexed as a context entry — it is never an error.

### Scope slug grammar

```
scope   = segment *( "/" segment )
segment = 1*( lowercase-letter / digit / "-" )
```

Examples: `order/cancel`, `payment/refund`, `auth`. Scopes are the primary
lookup axis for non-developer queries ("why does order cancellation work this
way?"), so name them after product concepts, not code paths. Teams SHOULD
maintain a shared scope list; the indexer treats unknown scopes as new.

## Audience level

Trailer values are written at **developer** language level. Do not write two
versions (developer + layperson) at commit time — audience translation is the
query layer's job (AI translates on read). This keeps write cost low and
avoids drift between two hand-maintained phrasings.

## What goes where

| Location       | Content                                                          |
| -------------- | ---------------------------------------------------------------- |
| Trailers       | Structured summary: why, scopes, decisions, refs. Per commit.    |
| Commit body    | Long-form narrative behind the trailers. Per commit.             |
| PR body        | Cross-commit discussion, review context. Indexed at merge time.  |
| Companion docs | Durable design records (ADRs). Linked via `Context-Ref`.         |

## Example

```
fix(order): delay refund until PG settlement is confirmed

Refunds fired immediately on cancellation caused double refunds when the
PG settlement was still pending. The refund now waits for the settlement
webhook before executing. Considered polling the PG status API instead,
but the webhook already carries the settlement event and polling would
add a scheduler dependency.

Context-Why: instant refund raced with pending PG settlement, causing double refunds
Context-Scope: order/cancel
Context-Scope: payment/refund
Context-Decision: settlement-webhook trigger over PG status polling; webhook already delivers the event
Context-Ref: https://github.com/example/shop/issues/123
```

## Parsing notes (implementers)

- Reference semantics: `git interpret-trailers --parse`. The reader MUST
  accept anything git parses as a trailer and apply the registry rules above.
- go-git does not ship a trailer parser; the indexer implements this grammar
  over the raw commit message. The trailer block is the last paragraph in
  which all lines match `key: value` (git also tolerates a small fraction of
  non-trailer lines; readers MAY be stricter and require all-trailer blocks).
- Line-level code↔commit mapping (`git log -L` equivalent) is out of scope
  for this format; it is an indexer concern.

## Versioning

This spec is versioned in its title. Additive changes (new optional keys) do
not bump the version. Breaking changes (semantics of existing keys) do.
