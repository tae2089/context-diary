// Package ingest is the shared indexing pipeline (docs/indexer-design.md
// X7-X21) used by both `context-diary index` and the serve webhook path:
// walk history since the cursor, map commits to entries, save atomically.
//
// @index Shared incremental indexing pipeline used by both the index CLI and the serve merge webhook.
package ingest

import (
	"context"
	"fmt"

	"github.com/tae2089/context-diary/internal/gitlog"
	"github.com/tae2089/context-diary/internal/index"
	"github.com/tae2089/context-diary/internal/store"
)

// Options selects what to ingest.
type Options struct {
	RepoPath string // filesystem path of the clone/mirror
	RepoName string // identity in the index
	Branch   string // empty = HEAD
	WalkFull bool   // full DAG instead of first-parent
	Rescan   bool   // ignore the cursor: rescan everything (backfill notes)
}

// Result reports what happened.
type Result struct {
	Inserted int
	Scanned  int
	Head     string
	Warnings []string // e.g. dropped invalid scopes
}

// Run executes one incremental ingest.
//
// @intent walk history since the cursor, map commits to entries, and save them — the shared path behind the index CLI and the serve merge webhook
// @domainRule Rescan ignores the stored cursor and rewalks the whole history so parser upgrades and edited backfill notes are reflected
// @sideEffect reads the git repository and writes the Postgres index (via store.SaveEntries)
// @see internal/store/store.go#SaveEntries
func Run(ctx context.Context, s *store.Store, opts Options) (Result, error) {
	repoID, cursor, err := s.UpsertRepo(ctx, opts.RepoName)
	if err != nil {
		return Result{}, err
	}
	if opts.Rescan {
		cursor = "" // full history; upsert-on-change dedups unchanged rows
	}

	walk := gitlog.Walk
	if opts.WalkFull {
		walk = gitlog.WalkFull
	}
	commits, head, err := walk(opts.RepoPath, opts.Branch, cursor)
	if err != nil {
		return Result{}, err
	}

	res := Result{Scanned: len(commits), Head: head}
	var entries []*index.Entry
	for _, c := range commits {
		if e := index.EntryFromCommit(c); e != nil {
			for _, dropped := range e.DroppedScopes {
				res.Warnings = append(res.Warnings,
					fmt.Sprintf("%s: dropped invalid scope %q", short(c.Hash), dropped))
			}
			entries = append(entries, e)
		}
	}

	res.Inserted, err = s.SaveEntries(ctx, repoID, entries, head, opts.Rescan)
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
