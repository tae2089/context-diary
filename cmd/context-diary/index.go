package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tae2089/context-diary/internal/gitx"
	"github.com/tae2089/context-diary/internal/ingest"
	"github.com/tae2089/context-diary/internal/store"
)

// dsnFromEnv resolves the Postgres DSN (design: env only, never config files).
func dsnFromEnv() string {
	if dsn := os.Getenv("CONTEXT_DIARY_DB"); dsn != "" {
		return dsn
	}
	return os.Getenv("DATABASE_URL")
}

// cmdIndex implements the ingestion flow (docs/indexer-design.md X1-X22).
// Interactive command: failures are loud (exit 1), unlike the hooks.
func cmdIndex(args []string) int {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	repoName := fs.String("repo", "", "repository name in the index (default: git top-level dir name)")
	branch := fs.String("branch", "", "branch to index (default: HEAD)")
	walk := fs.String("walk", "first-parent", "history walk: first-parent (squash/rebase teams) or full (merge-commit teams)")
	rescan := fs.Bool("rescan", false, "ignore the cursor and rescan the whole history (picks up edited backfill notes)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *walk != "first-parent" && *walk != "full" {
		warnf("invalid --walk %q (want first-parent or full)", *walk)
		return 2
	}

	dsn := dsnFromEnv()
	if dsn == "" {
		warnf("set CONTEXT_DIARY_DB (or DATABASE_URL) to a Postgres DSN")
		return 1
	}

	top, err := gitx.TopLevel(".")
	if err != nil {
		warnf("not a git repository? %v", err)
		return 1
	}
	if *repoName == "" {
		*repoName = filepath.Base(top)
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

	res, err := ingest.Run(ctx, s, ingest.Options{
		RepoPath: top,
		RepoName: *repoName,
		Branch:   *branch,
		WalkFull: *walk == "full",
		Rescan:   *rescan,
	})
	if err != nil {
		warnf("%v", err)
		return 1
	}
	for _, w := range res.Warnings {
		warnf("%s", w)
	}
	fmt.Printf("indexed %d entries (%d commits scanned) into %s @ %s\n",
		res.Inserted, res.Scanned, *repoName, short(res.Head))
	return 0
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
