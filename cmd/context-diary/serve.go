package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tae2089/context-diary/internal/forge/github"
	"github.com/tae2089/context-diary/internal/ingest"
	"github.com/tae2089/context-diary/internal/mcptool"
	"github.com/tae2089/context-diary/internal/mirror"
	"github.com/tae2089/context-diary/internal/preview"
	"github.com/tae2089/context-diary/internal/store"
)

const serveVersion = "0.1.0"

// serveDeps makes the webhook handler testable without network or DB.
type serveDeps struct {
	secret   []byte
	comment  func(ctx context.Context, fullName string, number int, body string) error
	ingestPR func(ctx context.Context, ev *github.PREvent) (ingest.Result, error)
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
	token := os.Getenv("GITHUB_TOKEN")
	secret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	missing := ""
	switch {
	case dsn == "":
		missing = "CONTEXT_DIARY_DB (or DATABASE_URL)"
	case token == "":
		missing = "GITHUB_TOKEN"
	case secret == "":
		missing = "GITHUB_WEBHOOK_SECRET"
	}
	if missing != "" {
		warnf("set %s", missing)
		return 1
	}
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

	gh := github.NewClient("", token)
	deps := serveDeps{
		secret: []byte(secret),
		comment: func(ctx context.Context, fullName string, number int, body string) error {
			return gh.UpsertComment(ctx, fullName, number, preview.Marker, body)
		},
		ingestPR: func(ctx context.Context, ev *github.PREvent) (ingest.Result, error) {
			path, err := mirror.Sync(*cacheDir, ev.FullName, ev.CloneURL, token)
			if err != nil {
				return ingest.Result{}, err
			}
			return ingest.Run(ctx, s, ingest.Options{
				RepoPath: path,
				RepoName: ev.FullName,
				Branch:   ev.DefaultBranch,
				WalkFull: *walk == "full",
			})
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook/github", webhookHandler(deps))
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcptool.NewServer(s, serveVersion)
	}, nil))
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
			fmt.Fprintln(w, "comment updated")
		case ev.Action == "closed" && ev.Merged:
			res, err := deps.ingestPR(r.Context(), ev)
			if err != nil {
				log.Printf("ingest %s: %v", ev.FullName, err)
				http.Error(w, "ingest failed", http.StatusInternalServerError)
				return
			}
			log.Printf("ingested %s: %d entries (%d scanned)", ev.FullName, res.Inserted, res.Scanned)
			fmt.Fprintf(w, "indexed %d entries\n", res.Inserted)
		default:
			fmt.Fprintln(w, "ignored")
		}
	}
}
