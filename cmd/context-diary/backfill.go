package main

import (
	"flag"
	"fmt"

	"github.com/tae2089/context-diary/internal/gitlog"
	"github.com/tae2089/context-diary/internal/gitx"
	"github.com/tae2089/context-diary/internal/index"
)

// cmdBackfill lists commits still lacking context (message and note alike),
// oldest first, as "hash<TAB>subject" — machine-readable input for an AI
// agent that writes the git notes (docs/backfill.md).
func cmdBackfill(args []string) int {
	fs := flag.NewFlagSet("backfill", flag.ContinueOnError)
	branch := fs.String("branch", "", "branch to scan (default: HEAD)")
	walk := fs.String("walk", "first-parent", "history walk: first-parent or full")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *walk != "first-parent" && *walk != "full" {
		warnf("invalid --walk %q (want first-parent or full)", *walk)
		return 2
	}

	top, err := gitx.TopLevel(".")
	if err != nil {
		warnf("not a git repository? %v", err)
		return 1
	}
	walkFn := gitlog.Walk
	if *walk == "full" {
		walkFn = gitlog.WalkFull
	}
	commits, _, err := walkFn(top, *branch, "")
	if err != nil {
		warnf("%v", err)
		return 1
	}

	candidates := 0
	for _, c := range commits {
		if index.EntryFromCommit(c) == nil {
			fmt.Printf("%s\t%s\n", c.Hash, firstLine(c.Message))
			candidates++
		}
	}
	warnf("%d of %d commits lack context; write notes per docs/backfill.md, then run 'context-diary index --rescan'", candidates, len(commits))
	return 0
}

func firstLine(s string) string {
	for i := range len(s) {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
