// Package gitlog walks a repository's first-parent history via go-git
// (docs/indexer-design.md X8-X12). go-git rather than the git CLI: the
// indexer may run where git is not installed (server container).
package gitlog

import (
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/tae2089/context-diary/internal/index"
)

// Walk returns the first-parent commits of branch after sinceHash
// (exclusive), oldest→newest, plus the branch head hash. An empty or
// unreachable sinceHash yields the full first-parent line (design X11:
// idempotent inserts make a rescan safe).
func Walk(repoPath, branch, sinceHash string) ([]index.Commit, string, error) {
	repo, err := git.PlainOpenWithOptions(repoPath, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, "", fmt.Errorf("open repo %s: %w", repoPath, err)
	}

	rev := "HEAD"
	if branch != "" {
		rev = branch
	}
	head, err := repo.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return nil, "", fmt.Errorf("resolve %s: %w", rev, err)
	}

	var newestFirst []index.Commit
	for hash := *head; ; {
		if hash.String() == sinceHash {
			break
		}
		c, err := repo.CommitObject(hash)
		if err != nil {
			return nil, "", fmt.Errorf("read commit %s: %w", hash, err)
		}
		newestFirst = append(newestFirst, index.Commit{
			Hash:        c.Hash.String(),
			Message:     c.Message,
			AuthorName:  c.Author.Name,
			AuthorEmail: c.Author.Email,
			CommittedAt: c.Committer.When.UTC(),
		})
		if c.NumParents() == 0 {
			break
		}
		parent, err := c.Parent(0) // first-parent line only (design L1)
		if err != nil {
			return nil, "", fmt.Errorf("parent of %s: %w", hash, err)
		}
		hash = parent.Hash
	}

	oldest := make([]index.Commit, len(newestFirst))
	for i, c := range newestFirst {
		oldest[len(newestFirst)-1-i] = c
	}
	return oldest, head.String(), nil
}
