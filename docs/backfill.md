# Backfill v0.1 — Context for Pre-Adoption History

Status: Draft
Depends on: [Trailer Format v0.1](trailer-format.md), [Indexer Design v0.1](indexer-design.md)

## Goal

An adopting repository has years of commits with no `Context-*` trailers, so
the index starts empty for existing code. Backfill attaches context to those
commits **without rewriting history**, using [git notes](https://git-scm.com/docs/git-notes)
on a dedicated ref:

```
refs/notes/context-diary
```

A note's content is a plain trailer block (no last-paragraph rule — the
whole note is trailers):

```
Context-Why: retry queue was added after the 2023 payment outage
Context-Scope: payment/retry
Context-Decision: at-least-once delivery over exactly-once; consumer is idempotent
```

## Precedence

Authored commit trailers always win. The note is consulted only when the
commit message has no non-empty `Context-Why`. Editing a note therefore
never overrides context an author wrote at commit time.

## Workflow (AI-agent driven)

Generation stays agent-delegated — the same principle as commit authoring:
the tool finds candidates and indexes results; an AI coding agent (Claude
Code, Codex, …) reads the history and writes the notes.

```sh
# 1. List commits lacking context (hash<TAB>subject, oldest first)
context-diary backfill

# 2. For each candidate the agent inspects the change and writes a note
git show <hash>
git notes --ref=context-diary add -m 'Context-Why: <reason>
Context-Scope: <scope>' <hash>

# 3. Share the notes (they do not push by default)
git push origin refs/notes/context-diary

# 4. Re-index: --rescan ignores the cursor; upsert-on-change makes
#    unchanged commits no-ops, so this is safe to repeat
context-diary index --rescan
```

Other clones fetch the notes with:

```sh
git fetch origin refs/notes/context-diary:refs/notes/context-diary
```

Suggested agent prompt: work oldest-first; state the problem the commit
solved, not what the diff does (same quality bar as the instructions
snippet); when the motivation is not recoverable from the diff and
surrounding history, write your best factual hypothesis and mark it
("likely …") rather than inventing certainty.

## Serve interaction

Mirrors are cloned with `--mirror` semantics, so `refs/notes/*` arrives with
every fetch. But the ingest cursor skips already-indexed commits — after a
backfill session, run `context-diary index --rescan` against the mirror (or
any clone with the notes ref) once. Merge-triggered ingests pick up notes
that arrived *before* the commits they annotate.

## Limitations

- Note edits after indexing need a `--rescan` to surface (no notes webhook).
- GitHub's UI does not render notes; the index and MCP answers are where
  backfilled context is visible.

## FAQ

**Q. Do backfill notes need to cover every old commit?**
No — coverage is incremental. `context-diary backfill` always shows what is
left; annotate the commits people actually ask about first.
