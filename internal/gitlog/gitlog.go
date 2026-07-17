// Package gitlog walks a repository's first-parent history via go-git
// (docs/indexer-design.md X8-X12). go-git rather than the git CLI: the
// indexer may run where git is not installed (server container).
package gitlog

import (
	"fmt"
	"io"
	"sort"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/tae2089/context-diary/internal/index"
)

// NotesRef is the git notes ref carrying backfilled context
// (docs/backfill.md): trailer lines attached to pre-adoption commits
// without rewriting history.
const NotesRef = "refs/notes/context-diary"

// WalkFull returns every commit reachable from branch but not from
// sinceHash — the whole DAG including side branches of merge commits —
// sorted oldest→newest by committer time. For merge-commit workflows
// (design L1 lift): side-branch commits carry their own trailers and are
// indexed individually. An empty or unreachable sinceHash yields the full
// DAG; over-collection is harmless (store dedups on conflict).
func WalkFull(repoPath, branch, sinceHash string) ([]index.Commit, string, error) {
	repo, err := openRepo(repoPath)
	if err != nil {
		return nil, "", err
	}
	notes, err := loadNotes(repo)
	if err != nil {
		return nil, "", err
	}

	rev := "HEAD"
	if branch != "" {
		rev = branch
	}
	head, err := repo.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return nil, "", fmt.Errorf("resolve %s: %w", rev, err)
	}

	// Exclusion set: everything already covered by the cursor.
	seen := map[plumbing.Hash]bool{}
	if sinceHash != "" {
		if cursor, err := repo.CommitObject(plumbing.NewHash(sinceHash)); err == nil {
			stack := []plumbing.Hash{cursor.Hash}
			for len(stack) > 0 {
				h := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if seen[h] {
					continue
				}
				seen[h] = true
				c, err := repo.CommitObject(h)
				if err != nil {
					return nil, "", fmt.Errorf("read commit %s: %w", h, err)
				}
				stack = append(stack, c.ParentHashes...)
			}
		} // unreachable cursor → empty set → full rescan (design X11)
	}

	var commits []index.Commit
	stack := []plumbing.Hash{*head}
	visited := map[plumbing.Hash]bool{}
	for len(stack) > 0 {
		h := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[h] || seen[h] {
			continue
		}
		visited[h] = true
		c, err := repo.CommitObject(h)
		if err != nil {
			return nil, "", fmt.Errorf("read commit %s: %w", h, err)
		}
		commits = append(commits, index.Commit{
			Hash:        c.Hash.String(),
			Message:     c.Message,
			Note:        notes[c.Hash.String()],
			AuthorName:  c.Author.Name,
			AuthorEmail: c.Author.Email,
			CommittedAt: c.Committer.When.UTC(),
		})
		stack = append(stack, c.ParentHashes...)
	}

	// Oldest→newest. Discovery order (DFS from head) reaches descendants
	// before ancestors, so its reverse breaks committer-time ties in
	// parents-first order.
	discovered := make(map[string]int, len(commits))
	for i, c := range commits {
		discovered[c.Hash] = i
	}
	sort.SliceStable(commits, func(i, j int) bool {
		if !commits[i].CommittedAt.Equal(commits[j].CommittedAt) {
			return commits[i].CommittedAt.Before(commits[j].CommittedAt)
		}
		return discovered[commits[i].Hash] > discovered[commits[j].Hash]
	})
	return commits, head.String(), nil
}

// Walk returns the first-parent commits of branch after sinceHash
// (exclusive), oldest→newest, plus the branch head hash. An empty or
// unreachable sinceHash yields the full first-parent line (design X11:
// idempotent inserts make a rescan safe).
func Walk(repoPath, branch, sinceHash string) ([]index.Commit, string, error) {
	repo, err := openRepo(repoPath)
	if err != nil {
		return nil, "", err
	}
	notes, err := loadNotes(repo)
	if err != nil {
		return nil, "", err
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
			Note:        notes[c.Hash.String()],
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

// openRepo opens a working-tree or bare repository (serve mirrors are bare;
// DetectDotGit alone cannot open those).
func openRepo(repoPath string) (*git.Repository, error) {
	if repo, err := git.PlainOpen(repoPath); err == nil {
		return repo, nil
	}
	repo, err := git.PlainOpenWithOptions(repoPath, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("open repo %s: %w", repoPath, err)
	}
	return repo, nil
}

// loadNotes reads the context notes ref into a commit-sha → note-text map.
// Git stores notes as a tree whose entries are named by the annotated
// commit's hex sha, either flat ("<40-hex>") or fanned out ("ab/<38-hex>",
// possibly nested); path segments concatenate to the sha. A missing ref
// yields an empty map.
func loadNotes(repo *git.Repository) (map[string]string, error) {
	ref, err := repo.Reference(plumbing.ReferenceName(NotesRef), true)
	if err != nil {
		return map[string]string{}, nil // no notes ref
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("read notes commit: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("read notes tree: %w", err)
	}
	notes := map[string]string{}
	if err := collectNotes(repo, tree, "", notes); err != nil {
		return nil, err
	}
	return notes, nil
}

func collectNotes(repo *git.Repository, tree *object.Tree, prefix string, notes map[string]string) error {
	for _, entry := range tree.Entries {
		name := prefix + entry.Name
		if entry.Mode.IsFile() {
			if len(name) != 40 {
				continue // non-note bookkeeping entry
			}
			blob, err := repo.BlobObject(entry.Hash)
			if err != nil {
				return fmt.Errorf("read note blob %s: %w", name, err)
			}
			r, err := blob.Reader()
			if err != nil {
				return fmt.Errorf("open note blob %s: %w", name, err)
			}
			b, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				return fmt.Errorf("read note %s: %w", name, err)
			}
			notes[name] = string(b)
			continue
		}
		sub, err := repo.TreeObject(entry.Hash)
		if err != nil {
			return fmt.Errorf("read notes subtree %s: %w", name, err)
		}
		if err := collectNotes(repo, sub, name, notes); err != nil {
			return err
		}
	}
	return nil
}
