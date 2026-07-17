package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tae2089/context-diary/internal/index"
)

// openTest connects to TEST_DATABASE_URL or skips. Each test gets a fresh
// schema by dropping known tables first.
func openTest(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	for _, tbl := range []string{"commit_details", "commit_scopes", "commits", "repos"} {
		if _, err := s.pool.Exec(ctx, "DROP TABLE IF EXISTS "+tbl+" CASCADE"); err != nil {
			t.Fatalf("drop %s: %v", tbl, err)
		}
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate must be idempotent: %v", err)
	}
	return s
}

func entry(hash, why string, at time.Time, scopes ...string) *index.Entry {
	return &index.Entry{
		Hash:        hash,
		Subject:     "subject " + hash,
		Message:     "subject " + hash + "\n\nContext-Why: " + why + "\n",
		AuthorName:  "t",
		AuthorEmail: "t@t.local",
		CommittedAt: at,
		Why:         why,
		Scopes:      scopes,
		Decisions:   []string{"d1 for " + hash, "d2 for " + hash},
		Refs:        []string{"https://example.com/" + hash},
	}
}

func TestSaveAndSearch(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	repoID, cursor, err := s.UpsertRepo(ctx, "acme/shop")
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}
	if cursor != "" {
		t.Errorf("fresh repo cursor = %q, want empty", cursor)
	}

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	entries := []*index.Entry{
		entry("aaa111", "refund raced with settlement", t0, "order/cancel", "payment/refund"),
		entry("bbb222", "kimchi inventory sync was stale", t1, "inventory"),
	}
	n, err := s.SaveEntries(ctx, repoID, entries, "bbb222", false)
	if err != nil {
		t.Fatalf("SaveEntries: %v", err)
	}
	if n != 2 {
		t.Errorf("inserted = %d, want 2", n)
	}

	// idempotency: same batch again → 0 new rows, cursor still updates
	n, err = s.SaveEntries(ctx, repoID, entries, "bbb222", false)
	if err != nil {
		t.Fatalf("SaveEntries rerun: %v", err)
	}
	if n != 0 {
		t.Errorf("rerun inserted = %d, want 0", n)
	}

	_, cursor, err = s.UpsertRepo(ctx, "acme/shop")
	if err != nil {
		t.Fatal(err)
	}
	if cursor != "bbb222" {
		t.Errorf("cursor = %q, want bbb222", cursor)
	}

	// by scope
	rs, err := s.Search(ctx, "acme/shop", Query{Scope: "payment/refund"})
	if err != nil {
		t.Fatalf("Search scope: %v", err)
	}
	if len(rs) != 1 || rs[0].Hash != "aaa111" {
		t.Errorf("scope search = %+v", rs)
	}
	if len(rs) == 1 {
		if rs[0].Why == "" || len(rs[0].Scopes) != 2 || len(rs[0].Decisions) != 2 || len(rs[0].Refs) != 1 {
			t.Errorf("result not fully hydrated: %+v", rs[0])
		}
	}

	// by text
	rs, err = s.Search(ctx, "acme/shop", Query{Text: "kimchi"})
	if err != nil {
		t.Fatalf("Search text: %v", err)
	}
	if len(rs) != 1 || rs[0].Hash != "bbb222" {
		t.Errorf("text search = %+v", rs)
	}

	// by time window
	rs, err = s.Search(ctx, "acme/shop", Query{Since: t1.Add(-time.Hour)})
	if err != nil {
		t.Fatalf("Search time: %v", err)
	}
	if len(rs) != 1 || rs[0].Hash != "bbb222" {
		t.Errorf("time search = %+v", rs)
	}

	// no filters → newest first
	rs, err = s.Search(ctx, "acme/shop", Query{})
	if err != nil {
		t.Fatalf("Search all: %v", err)
	}
	if len(rs) != 2 || rs[0].Hash != "bbb222" || rs[0].Repo != "acme/shop" {
		t.Errorf("all search order = %+v", rs)
	}

	// empty repo → cross-repo search
	rs, err = s.Search(ctx, "", Query{})
	if err != nil {
		t.Fatalf("Search cross-repo: %v", err)
	}
	if len(rs) != 2 {
		t.Errorf("cross-repo search = %d results", len(rs))
	}

	// Korean: agglutinated forms must match their stem query. FTS 'simple'
	// tokenizes "환불이" as one token, so this requires trigram matching.
	korean := entry("ccc333", "환불이 정산보다 먼저 실행되어 중복 환불 발생", t1.Add(time.Hour), "payment/refund")
	if _, err := s.SaveEntries(ctx, repoID, []*index.Entry{korean}, "ccc333", false); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"환불", "정산", "중복 환불"} {
		rs, err = s.Search(ctx, "acme/shop", Query{Text: q})
		if err != nil {
			t.Fatalf("Search %q: %v", q, err)
		}
		found := false
		for _, r := range rs {
			if r.Hash == "ccc333" {
				found = true
			}
		}
		if !found {
			t.Errorf("Korean query %q did not match agglutinated content", q)
		}
	}
	rs, err = s.Search(ctx, "acme/shop", Query{Text: "송장번호"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if r.Hash == "ccc333" {
			t.Error("unrelated Korean query matched")
		}
	}

	// upsert-on-change: edited content (e.g. backfill note) refreshes the row
	changed := entry("aaa111", "refund raced with settlement — corrected wording", t0, "order/refund")
	n, err = s.SaveEntries(ctx, repoID, []*index.Entry{changed}, "bbb222", false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("changed entry upsert = %d, want 1", n)
	}
	rs, err = s.Search(ctx, "acme/shop", Query{Scope: "order/refund"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || !strings.Contains(rs[0].Why, "corrected wording") || len(rs[0].Scopes) != 1 {
		t.Errorf("upserted entry not refreshed: %+v", rs)
	}
	if rs2, _ := s.Search(ctx, "acme/shop", Query{Scope: "order/cancel"}); len(rs2) != 0 {
		t.Error("stale scope survived children rebuild")
	}

	// ByHashes: only matching hashes hydrate, oldest first
	hs, err := s.ByHashes(ctx, "acme/shop", []string{"aaa111", "bbb222", "not-indexed"})
	if err != nil {
		t.Fatalf("ByHashes: %v", err)
	}
	if len(hs) != 2 || hs[0].Hash != "aaa111" || hs[1].Hash != "bbb222" {
		t.Errorf("ByHashes = %+v", hs)
	}

	// cross-repo joins: code ref + text ref
	repoB, _, err := s.UpsertRepo(ctx, "acme/billing")
	if err != nil {
		t.Fatal(err)
	}
	crossEntry := entry("ddd444", "billing must respect shop refund waits", t1.Add(2*time.Hour), "payment/refund")
	crossEntry.Refs = []string{"https://jira.example.com/browse/PAY-77", "acme/shop//internal/refund/refund.go#ProcessRefund"}
	crossEntry.CodeRefs = []index.CodeRef{{Repo: "acme/shop", Path: "internal/refund/refund.go", Symbol: "ProcessRefund"}}
	if _, err := s.SaveEntries(ctx, repoB, []*index.Entry{crossEntry}, "ddd444", false); err != nil {
		t.Fatal(err)
	}

	refs, err := s.ReferencedBy(ctx, "acme/shop", "internal/refund/refund.go", "ProcessRefund")
	if err != nil {
		t.Fatalf("ReferencedBy: %v", err)
	}
	if len(refs) != 1 || refs[0].Repo != "acme/billing" || refs[0].Hash != "ddd444" {
		t.Errorf("ReferencedBy = %+v (billing entry must point at shop function)", refs)
	}
	// path-level ref (empty symbol) must also match any symbol query
	if refs, _ := s.ReferencedBy(ctx, "acme/shop", "internal/refund/refund.go", "OtherFunc"); len(refs) != 0 {
		t.Errorf("symbol-specific ref matched wrong symbol: %+v", refs)
	}

	byText, err := s.ByRefText(ctx, "PAY-77")
	if err != nil {
		t.Fatalf("ByRefText: %v", err)
	}
	if len(byText) != 1 || byText[0].Repo != "acme/billing" {
		t.Errorf("ByRefText = %+v", byText)
	}

	scopes, err := s.ListScopes(ctx, "acme/shop")
	if err != nil {
		t.Fatalf("ListScopes: %v", err)
	}
	if len(scopes) != 3 || scopes[0].Scope != "inventory" || scopes[0].Count != 1 {
		t.Errorf("scopes = %+v", scopes)
	}
}
