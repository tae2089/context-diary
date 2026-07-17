package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/tae2089/context-diary/internal/funclog"
	"github.com/tae2089/context-diary/internal/gitx"
	"github.com/tae2089/context-diary/internal/store"
)

// cmdExplain prints the why-timeline of one function: git line-level
// history joined with the context index (same composition as the
// explain_function MCP tool, for local use).
func cmdExplain(args []string) int {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	repoName := fs.String("repo", "", "repository name in the index (default: git top-level dir name)")
	branch := fs.String("branch", "", "branch to trace (default: HEAD)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Println("usage: context-diary explain [--repo <name>] <file> <function>")
		return 2
	}
	file, function := fs.Arg(0), fs.Arg(1)

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

	commits, err := funclog.CommitsTouching(top, *branch, file, function)
	if err != nil {
		warnf("%v", err)
		return 1
	}
	if len(commits) == 0 {
		fmt.Printf("no history found for %s in %s\n", function, file)
		return 0
	}

	ctx := context.Background()
	s, err := store.Open(ctx, dsn)
	if err != nil {
		warnf("%v", err)
		return 1
	}
	defer s.Close()

	hashes := make([]string, len(commits))
	for i, c := range commits {
		hashes[i] = c.Hash
	}
	indexed, err := s.ByHashes(ctx, *repoName, hashes)
	if err != nil {
		warnf("%v", err)
		return 1
	}
	byHash := map[string]store.Result{}
	for _, r := range indexed {
		byHash[r.Hash] = r
	}

	fmt.Printf("%s — %s: %d change(s)\n\n", file, function, len(commits))
	for _, c := range commits {
		fmt.Printf("%s  %s\n", short(c.Hash), c.Subject)
		r, ok := byHash[c.Hash]
		if !ok {
			fmt.Printf("    (no context — candidate for backfill, see docs/backfill.md)\n\n")
			continue
		}
		fmt.Printf("    why: %s\n", r.Why)
		for _, d := range r.Decisions {
			fmt.Printf("    decision: %s\n", d)
		}
		fmt.Println()
	}
	return 0
}
