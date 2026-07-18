package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tae2089/context-diary/internal/store"
)

type fakeStore struct {
	lastQuery store.Query
	lastRepo  string
	total     int // total rows available; Search honors Limit/Offset over this
}

func (f *fakeStore) Search(_ context.Context, repo string, q store.Query) ([]store.Result, error) {
	f.lastRepo, f.lastQuery = repo, q
	total := f.total
	if total == 0 {
		total = 1
	}
	var out []store.Result
	for i := q.Offset; i < total && len(out) < q.Limit; i++ {
		out = append(out, store.Result{
			Repo:        "tae2089/context-diary",
			Hash:        "abc1234567890",
			Subject:     "feat: add <script>alert(1)</script> indexer",
			Why:         "환불이 정산보다 먼저 실행되어 중복 환불 발생",
			AuthorName:  "tae2089",
			CommittedAt: time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC),
			Scopes:      []string{"payment/refund"},
			Decisions:   []string{"webhook over polling; simpler"},
			Refs:        []string{"https://example.com/pm-42"},
		})
	}
	return out, nil
}

func (f *fakeStore) ListScopes(context.Context, string) ([]store.ScopeCount, error) {
	return []store.ScopeCount{{Scope: "payment/refund", Count: 3}, {Scope: "docs/readme", Count: 1}}, nil
}

func (f *fakeStore) ListRepos(context.Context) ([]string, error) {
	return []string{"tae2089/context-diary", "acme/shop"}, nil
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestHomeRendersEntriesAndScopes(t *testing.T) {
	fs := &fakeStore{}
	h := NewHandler(fs)
	w := get(t, h, "/ui/")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"환불이 정산보다",       // why rendered
		"payment/refund", // scope chip
		"(3)",            // scope count
		"abc1234",        // short hash
		"webhook over polling",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home missing %q", want)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("unescaped subject (XSS)")
	}
	if !strings.Contains(body, "github.com/tae2089/context-diary/commit/abc1234567890") {
		t.Error("missing forge commit link for owner/repo entries")
	}
}

func TestSearchPassesFilters(t *testing.T) {
	fs := &fakeStore{}
	h := NewHandler(fs)
	w := get(t, h, "/ui/search?q=%ED%99%98%EB%B6%88&scope=payment/refund&repo=acme/shop")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if fs.lastQuery.Text != "환불" || fs.lastQuery.Scope != "payment/refund" || fs.lastRepo != "acme/shop" {
		t.Errorf("filters not passed: repo=%q q=%+v", fs.lastRepo, fs.lastQuery)
	}
	if !strings.Contains(w.Body.String(), "환불") {
		t.Error("query not echoed in form")
	}
}

func TestPagination(t *testing.T) {
	// 45 rows, pageSize 20 → page 1 has next, no prev; page 3 has prev, no next.
	fs := &fakeStore{total: 45}
	h := NewHandler(fs)

	w := get(t, h, "/ui/")
	if fs.lastQuery.Offset != 0 || fs.lastQuery.Limit != pageSize+1 {
		t.Errorf("page 1 query = %+v (want offset 0, limit %d)", fs.lastQuery, pageSize+1)
	}
	body := w.Body.String()
	if !strings.Contains(body, "page=2") {
		t.Error("page 1 must link next(2)")
	}
	if strings.Contains(body, `href="/ui/search?page=0`) {
		t.Error("page 1 must not offer a prev link")
	}

	w = get(t, h, "/ui/search?scope=payment/refund&page=2")
	if fs.lastQuery.Offset != pageSize {
		t.Errorf("page 2 offset = %d, want %d", fs.lastQuery.Offset, pageSize)
	}
	body = w.Body.String()
	if !strings.Contains(body, "page=1") || !strings.Contains(body, "page=3") {
		t.Errorf("page 2 must link prev(1) and next(3):\n%s", body)
	}
	if !strings.Contains(body, "scope=payment%2Frefund") {
		t.Error("page links must preserve the scope filter")
	}

	w = get(t, h, "/ui/search?page=3")
	body = w.Body.String()
	if !strings.Contains(body, "page=2") {
		t.Error("page 3 must link prev(2)")
	}
	if strings.Contains(body, "page=4") {
		t.Error("page 3 is the last page; must not offer next")
	}
}
