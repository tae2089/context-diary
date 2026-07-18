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

## Ref forms

`Context-Ref` values are one line of free text, but readers interpret three
forms (additive — older readers treat them all as opaque text):

| Form | Example | Reader behavior |
| --- | --- | --- |
| URL | `https://wiki.example.com/postmortem-42` | text-searchable join key |
| Issue ID | `JIRA-123` | text-searchable join key |
| Code ref | `owner/repo//path/to/file.go#Symbol` | parsed structurally: enables reverse lookup ("which entries anywhere reference this function") |

Code ref grammar: repository full name, a literal `//`, the file path, and
an optional `#Symbol` (function/method name). GitHub blob URLs also parse
as code refs (repo + path only — `#L10` line fragments rot with edits and
are ignored). Use refs as the cross-repository join: entries in different
repositories sharing a ticket URL, or pointing at each other's functions,
become one traceable piece of work.

## Scopes across repositories

Scopes are product concepts, so they intentionally cross repository
boundaries: `payment/refund` in both order-service and payment-service is
the SAME scope, and a scope query spans repos. Maintain ONE shared scope
dictionary for the organization (each repo's `.context-diary.toml` scopes
list drawn from it) — inconsistent slugs (`payment/refund` vs `refunds`)
break the join.

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
  over the raw commit message. Readers treat the run of CONSECUTIVE
  all-trailer paragraphs at the end of the message as one trailer block —
  more lenient than git's last-paragraph rule, deliberately: GitHub's
  squash merge appends `Co-authored-by` as its own final paragraph, which
  would otherwise push the Context trailers written in the PR description
  into the body and silently unindex them (observed in production on the
  first squash-merged PR).
- Line-level code↔commit mapping (`git log -L` equivalent) is out of scope
  for this format; it is an indexer concern.

## Versioning

This spec is versioned in its title. Additive changes (new optional keys) do
not bump the version. Breaking changes (semantics of existing keys) do.
