package index

import (
	"reflect"
	"testing"
	"time"
)

func meta(msg string) Commit {
	return Commit{
		Hash:        "abc123",
		Message:     msg,
		AuthorName:  "t",
		AuthorEmail: "t@example.com",
		CommittedAt: time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
	}
}

func TestEntryFromCommit(t *testing.T) {
	msg := `fix(order): delay refund

Long body explaining things.

Context-Why: refund raced with settlement
Context-Scope: order/cancel
Context-Scope: payment/refund
Context-Decision: webhook over polling; event already delivered
Context-Ref: https://example.com/issues/1
Context-Decision: second decision
`
	e := EntryFromCommit(meta(msg))
	if e == nil {
		t.Fatal("EntryFromCommit returned nil for a context commit")
	}
	if e.Subject != "fix(order): delay refund" {
		t.Errorf("subject = %q", e.Subject)
	}
	if e.Why != "refund raced with settlement" {
		t.Errorf("why = %q", e.Why)
	}
	if !reflect.DeepEqual(e.Scopes, []string{"order/cancel", "payment/refund"}) {
		t.Errorf("scopes = %v", e.Scopes)
	}
	if !reflect.DeepEqual(e.Decisions, []string{
		"webhook over polling; event already delivered",
		"second decision",
	}) {
		t.Errorf("decisions = %v (order must be kept)", e.Decisions)
	}
	if !reflect.DeepEqual(e.Refs, []string{"https://example.com/issues/1"}) {
		t.Errorf("refs = %v", e.Refs)
	}
	if e.Hash != "abc123" || e.CommittedAt.IsZero() {
		t.Errorf("metadata not carried: %+v", e)
	}
}

func TestEntryFromCommitSkipsNonContext(t *testing.T) {
	for name, msg := range map[string]string{
		"no trailers":  "chore: bump deps\n\nbody\n",
		"empty why":    "chore: x\n\nContext-Why:\nContext-Scope: a\n",
		"only scope":   "chore: x\n\nContext-Scope: a\n",
		"empty msg":    "",
		"subject only": "chore: x\n",
	} {
		if e := EntryFromCommit(meta(msg)); e != nil {
			t.Errorf("%s: expected nil, got %+v", name, e)
		}
	}
}

func TestEntryFromCommitNoteBackfill(t *testing.T) {
	// note provides context for a commit that has none
	c := meta("chore: legacy change\n\nold body\n")
	c.Note = "Context-Why: backfilled reason\nContext-Scope: legacy/area\nContext-Decision: kept as-is; risk too high\n"
	e := EntryFromCommit(c)
	if e == nil {
		t.Fatal("note-backfilled commit not indexed")
	}
	if e.Why != "backfilled reason" {
		t.Errorf("why = %q", e.Why)
	}
	if len(e.Scopes) != 1 || e.Scopes[0] != "legacy/area" || len(e.Decisions) != 1 {
		t.Errorf("note trailers not applied: %+v", e)
	}

	// commit trailers win: note is ignored when the message has Context-Why
	c2 := meta("fix: x\n\nContext-Why: authored reason\nContext-Scope: real/scope\n")
	c2.Note = "Context-Why: stale note\nContext-Scope: wrong/scope\n"
	e2 := EntryFromCommit(c2)
	if e2 == nil || e2.Why != "authored reason" {
		t.Fatalf("commit trailers must win: %+v", e2)
	}
	for _, s := range e2.Scopes {
		if s == "wrong/scope" {
			t.Error("note scope leaked into authored entry")
		}
	}

	// note without Context-Why does not index the commit
	c3 := meta("chore: y\n\nbody\n")
	c3.Note = "Context-Scope: only/scope\n"
	if e3 := EntryFromCommit(c3); e3 != nil {
		t.Errorf("scope-only note should not index: %+v", e3)
	}
}

func TestEntryFromCommitScopeHygiene(t *testing.T) {
	msg := "fix: x\n\nContext-Why: y\nContext-Scope: order/cancel\nContext-Scope: order/cancel\nContext-Scope: Bad Slug\n"
	e := EntryFromCommit(meta(msg))
	if e == nil {
		t.Fatal("nil entry")
	}
	if !reflect.DeepEqual(e.Scopes, []string{"order/cancel"}) {
		t.Errorf("scopes = %v, want deduped valid-only", e.Scopes)
	}
	if !reflect.DeepEqual(e.DroppedScopes, []string{"Bad Slug"}) {
		t.Errorf("dropped = %v", e.DroppedScopes)
	}
}
