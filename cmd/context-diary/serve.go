package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tae2089/context-diary/internal/forge/github"
	"github.com/tae2089/context-diary/internal/ingest"
	"github.com/tae2089/context-diary/internal/mcptool"
	"github.com/tae2089/context-diary/internal/mirror"
	"github.com/tae2089/context-diary/internal/preview"
	"github.com/tae2089/context-diary/internal/queue"
	"github.com/tae2089/context-diary/internal/store"
	"github.com/tae2089/context-diary/internal/trailer"
)

const serveVersion = "0.1.0"

// Status contexts shown in the GitHub UI.
const (
	statusContextLint   = "context-diary/context"
	statusContextIngest = "context-diary/ingest"
)

// serveDeps makes the webhook handler testable without network or DB.
type serveDeps struct {
	secret  []byte
	comment func(ctx context.Context, fullName string, number int, body string) error
	status  func(ctx context.Context, fullName, sha, state, statusContext, description string) error
	// enqueue schedules async ingestion for a merged PR; false = queue full.
	enqueue func(ev *github.PREvent) bool
}

// cmdServe runs the GitHub PR bot + MCP endpoint (docs/serve-design.md).
func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "listen address")
	cacheDir := fs.String("cache-dir", "", "bare mirror cache (default: user cache dir)")
	walk := fs.String("walk", "first-parent", "history walk: first-parent or full")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *walk != "first-parent" && *walk != "full" {
		warnf("invalid --walk %q (want first-parent or full)", *walk)
		return 2
	}

	dsn := dsnFromEnv()
	secret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	switch {
	case dsn == "":
		warnf("set CONTEXT_DIARY_DB (or DATABASE_URL)")
		return 1
	case secret == "":
		warnf("set GITHUB_WEBHOOK_SECRET")
		return 1
	}
	tokenFn, authKind, err := githubTokenFn()
	if err != nil {
		warnf("%v", err)
		return 1
	}
	log.Printf("github auth: %s", authKind)
	if *cacheDir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			warnf("cannot resolve cache dir: %v", err)
			return 1
		}
		*cacheDir = filepath.Join(base, "context-diary", "repos")
	}

	ctx := context.Background()
	s, err := store.Open(ctx, dsn)
	if err != nil {
		warnf("%v", err)
		return 1
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		warnf("%v", err)
		return 1
	}

	gh := github.NewClientWithTokenFunc("", tokenFn)

	// Pending merged-PR events keyed by repo; the queue carries keys only,
	// per-repo FIFO of events lives here.
	var pmu sync.Mutex
	pending := map[string][]*github.PREvent{}

	runIngest := func(ctx context.Context, key string) {
		pmu.Lock()
		evs := pending[key]
		delete(pending, key)
		pmu.Unlock()
		for _, ev := range evs {
			// Installation tokens rotate hourly; resolve at job time, not enqueue time.
			token, err := tokenFn(ctx)
			var path string
			if err == nil {
				path, err = mirror.Sync(*cacheDir, ev.FullName, ev.CloneURL, token)
			}
			var res ingest.Result
			if err == nil {
				res, err = ingest.Run(ctx, s, ingest.Options{
					RepoPath: path,
					RepoName: ev.FullName,
					Branch:   ev.DefaultBranch,
					WalkFull: *walk == "full",
				})
			}
			state, desc := github.StatusSuccess, fmt.Sprintf("indexed %d entries (%d scanned)", res.Inserted, res.Scanned)
			if err != nil {
				state, desc = github.StatusError, err.Error()
				log.Printf("ingest %s: %v", ev.FullName, err)
			} else {
				log.Printf("ingested %s: %d entries (%d scanned)", ev.FullName, res.Inserted, res.Scanned)
			}
			if ev.MergeCommitSHA != "" {
				if serr := gh.SetStatus(ctx, ev.FullName, ev.MergeCommitSHA, state, statusContextIngest, desc); serr != nil {
					log.Printf("set ingest status %s: %v", ev.FullName, serr)
				}
			}
		}
	}
	q := queue.New(4, 256, runIngest)
	q.Start(ctx)

	deps := serveDeps{
		secret: []byte(secret),
		comment: func(ctx context.Context, fullName string, number int, body string) error {
			return gh.UpsertComment(ctx, fullName, number, preview.Marker, body)
		},
		status: gh.SetStatus,
		enqueue: func(ev *github.PREvent) bool {
			pmu.Lock()
			pending[ev.FullName] = append(pending[ev.FullName], ev)
			pmu.Unlock()
			if q.Enqueue(ev.FullName) {
				return true
			}
			// roll back the pending entry we just added
			pmu.Lock()
			evs := pending[ev.FullName]
			if len(evs) > 0 {
				pending[ev.FullName] = evs[:len(evs)-1]
			}
			pmu.Unlock()
			return false
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook/github", webhookHandler(deps))
	repoPath := func(repo string) (string, error) {
		path := mirror.Path(*cacheDir, repo)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("no mirror for %q yet — it appears after the first merged PR (or run 'context-diary index' against a clone)", repo)
		}
		return path, nil
	}
	var mcpHandler http.Handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcptool.NewServer(mcptool.Deps{Store: s, RepoPath: repoPath, Version: serveVersion})
	}, nil)
	if mcpToken := os.Getenv("CONTEXT_DIARY_MCP_TOKEN"); mcpToken != "" {
		mcpHandler = bearerAuth(mcpToken, mcpHandler)
	} else {
		log.Printf("warning: CONTEXT_DIARY_MCP_TOKEN not set — /mcp is unauthenticated; deploy inside a trusted network only")
	}
	mux.Handle("/mcp", mcpHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("context-diary serve listening on %s (webhook: /webhook/github, mcp: /mcp)", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		warnf("%v", err)
		return 1
	}
	return 0
}

// githubTokenFn selects the auth mode: a PAT when GITHUB_TOKEN is set,
// otherwise GitHub App credentials (GITHUB_APP_ID, GITHUB_APP_INSTALLATION_ID,
// GITHUB_APP_PRIVATE_KEY or _FILE). App auth is preferred for real
// deployments: per-repo installation scope, hourly-rotating tokens.
func githubTokenFn() (func(context.Context) (string, error), string, error) {
	if pat := os.Getenv("GITHUB_TOKEN"); pat != "" {
		return func(context.Context) (string, error) { return pat, nil }, "personal access token", nil
	}
	appID := os.Getenv("GITHUB_APP_ID")
	instID := os.Getenv("GITHUB_APP_INSTALLATION_ID")
	keyPEM := os.Getenv("GITHUB_APP_PRIVATE_KEY")
	if keyPEM == "" {
		if path := os.Getenv("GITHUB_APP_PRIVATE_KEY_FILE"); path != "" {
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, "", fmt.Errorf("read GITHUB_APP_PRIVATE_KEY_FILE: %w", err)
			}
			keyPEM = string(b)
		}
	}
	if appID == "" || instID == "" || keyPEM == "" {
		return nil, "", errors.New("set GITHUB_TOKEN, or GITHUB_APP_ID + GITHUB_APP_INSTALLATION_ID + GITHUB_APP_PRIVATE_KEY(_FILE)")
	}
	app, err := github.NewAppAuth("", appID, instID, keyPEM)
	if err != nil {
		return nil, "", err
	}
	return app.Token, fmt.Sprintf("github app %s (installation %s)", appID, instID), nil
}

// bearerAuth guards a handler with a constant-time bearer token check.
func bearerAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="context-diary"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bodyClean reports whether a PR description passes the trailer lint
// (same composition rule as lint-message: synthetic subject + body).
func bodyClean(prBody string) bool {
	return len(trailer.Lint("subject\n\n"+prBody)) == 0
}

// webhookHandler implements flows W1-W9 and M1-M7 of docs/serve-design.md.
func webhookHandler(deps serveDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if !github.ValidSignature(deps.secret, body, r.Header.Get("X-Hub-Signature-256")) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-GitHub-Event") != "pull_request" {
			fmt.Fprintln(w, "ignored")
			return
		}
		ev, err := github.ParsePREvent(body)
		if err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}

		switch {
		case ev.Action == "opened" || ev.Action == "edited" ||
			ev.Action == "reopened" || ev.Action == "synchronize":
			if err := deps.comment(r.Context(), ev.FullName, ev.Number, preview.Comment(ev.Body)); err != nil {
				log.Printf("comment on %s#%d: %v", ev.FullName, ev.Number, err)
				http.Error(w, "comment failed", http.StatusBadGateway)
				return
			}
			// Lint status on the head SHA lets branch protection require it.
			if ev.HeadSHA != "" {
				state, desc := github.StatusSuccess, "context trailers present"
				if !bodyClean(ev.Body) {
					state, desc = github.StatusFailure, "PR description is missing context trailers (see bot comment)"
				}
				if err := deps.status(r.Context(), ev.FullName, ev.HeadSHA, state, statusContextLint, desc); err != nil {
					log.Printf("set lint status %s#%d: %v", ev.FullName, ev.Number, err)
				}
			}
			fmt.Fprintln(w, "comment updated")
		case ev.Action == "closed" && ev.Merged:
			if !deps.enqueue(ev) {
				http.Error(w, "ingest queue full, retry later", http.StatusServiceUnavailable)
				return
			}
			if ev.MergeCommitSHA != "" {
				if err := deps.status(r.Context(), ev.FullName, ev.MergeCommitSHA,
					github.StatusPending, statusContextIngest, "ingest queued"); err != nil {
					log.Printf("set pending status %s: %v", ev.FullName, err)
				}
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprintln(w, "ingest queued")
		default:
			fmt.Fprintln(w, "ignored")
		}
	}
}
