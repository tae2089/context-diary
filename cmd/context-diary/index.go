package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tae2089/context-diary/internal/gitlog"
	"github.com/tae2089/context-diary/internal/gitx"
	"github.com/tae2089/context-diary/internal/index"
	"github.com/tae2089/context-diary/internal/store"
)

// cmdIndex implements the ingestion flow (docs/indexer-design.md X1-X22).
// Interactive command: failures are loud (exit 1), unlike the hooks.
func cmdIndex(args []string) int {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	repoName := fs.String("repo", "", "repository name in the index (default: git top-level dir name)")
	branch := fs.String("branch", "", "branch to index (default: HEAD)")
	walk := fs.String("walk", "first-parent", "history walk: first-parent (squash/rebase teams) or full (merge-commit teams)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *walk != "first-parent" && *walk != "full" {
		warnf("invalid --walk %q (want first-parent or full)", *walk)
		return 2
	}

	dsn := os.Getenv("CONTEXT_DIARY_DB")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
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

	repoID, cursor, err := s.UpsertRepo(ctx, *repoName)
	if err != nil {
		warnf("%v", err)
		return 1
	}

	walkFn := gitlog.Walk
	if *walk == "full" {
		walkFn = gitlog.WalkFull
	}
	commits, head, err := walkFn(top, *branch, cursor)
	if err != nil {
		warnf("%v", err)
		return 1
	}

	var entries []*index.Entry
	for _, c := range commits {
		if e := index.EntryFromCommit(c); e != nil {
			for _, dropped := range e.DroppedScopes {
				warnf("%s: dropped invalid scope %q", c.Hash[:12], dropped)
			}
			entries = append(entries, e)
		}
	}

	inserted, err := s.SaveEntries(ctx, repoID, entries, head)
	if err != nil {
		warnf("%v", err)
		return 1
	}
	fmt.Printf("indexed %d entries (%d commits scanned) into %s @ %s\n",
		inserted, len(commits), *repoName, short(head))
	return 0
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
