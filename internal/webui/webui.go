// Package webui is the read-only web interface over the context index
// (docs/serve-design.md §Web UI): server-rendered html/template, no
// JavaScript required, embedded in the serve binary. Same trust posture as
// /mcp — deploy inside the network boundary.
package webui

import (
	"context"
	"embed"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tae2089/context-diary/internal/store"
)

// Store is the query surface the UI needs (consumer-owned interface).
type Store interface {
	Search(ctx context.Context, repoName string, q store.Query) ([]store.Result, error)
	ListScopes(ctx context.Context, repoName string) ([]store.ScopeCount, error)
	ListRepos(ctx context.Context) ([]string, error)
}

//go:embed templates/*.html
var templateFS embed.FS

var ownerRepoRe = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

var funcs = template.FuncMap{
	"short": func(hash string) string {
		if len(hash) > 12 {
			return hash[:12]
		}
		return hash
	},
	"date": func(t time.Time) string { return t.Local().Format("2006-01-02 15:04") },
	"commitURL": func(repo, hash string) string {
		if ownerRepoRe.MatchString(repo) {
			return "https://github.com/" + repo + "/commit/" + hash
		}
		return ""
	},
}

var pages = template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html"))

type handler struct {
	store Store
}

// NewHandler mounts the UI under /ui/.
func NewHandler(s Store) http.Handler {
	h := &handler{store: s}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ui/{$}", h.home)
	mux.HandleFunc("GET /ui/search", h.search)
	return mux
}

type pageData struct {
	Title   string
	Query   string
	Scope   string
	Repo    string
	Repos   []string
	Scopes  []store.ScopeCount
	Entries []store.Result
	Err     string
}

func (h *handler) render(w http.ResponseWriter, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages.ExecuteTemplate(w, "page.html", data); err != nil {
		log.Printf("webui render: %v", err)
	}
}

func (h *handler) home(w http.ResponseWriter, r *http.Request) {
	h.results(w, r, pageData{Title: "context-diary"})
}

func (h *handler) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	h.results(w, r, pageData{
		Title: "search — context-diary",
		Query: strings.TrimSpace(q.Get("q")),
		Scope: strings.TrimSpace(q.Get("scope")),
		Repo:  strings.TrimSpace(q.Get("repo")),
	})
}

func (h *handler) results(w http.ResponseWriter, r *http.Request, data pageData) {
	ctx := r.Context()
	var err error
	if data.Scopes, err = h.store.ListScopes(ctx, data.Repo); err != nil {
		data.Err = err.Error()
	}
	if data.Repos, err = h.store.ListRepos(ctx); err != nil {
		data.Err = err.Error()
	}
	entries, err := h.store.Search(ctx, data.Repo, store.Query{
		Text:  data.Query,
		Scope: data.Scope,
		Limit: 50,
	})
	if err != nil {
		data.Err = err.Error()
	}
	data.Entries = entries
	h.render(w, data)
}
