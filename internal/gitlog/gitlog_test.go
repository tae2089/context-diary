package gitlog

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildRepo creates a repo with three commits and returns its path plus the
// three hashes oldest→newest.
func buildRepo(t *testing.T) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.local",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.local",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q", "-b", "main")
	var hashes []string
	for i, msg := range []string{
		"first\n\nContext-Why: reason one\n",
		"second: no trailers\n",
		"third\n\nContext-Why: reason three\nContext-Scope: a/b\n",
	} {
		name := filepath.Join(dir, "f"+string(rune('0'+i)))
		if err := os.WriteFile(name, []byte(msg), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", ".")
		git("commit", "-q", "-m", msg)
		hashes = append(hashes, string([]byte(git("rev-parse", "HEAD"))[:40]))
	}
	return dir, hashes
}

func TestWalkFull(t *testing.T) {
	dir, hashes := buildRepo(t)
	commits, head, err := Walk(dir, "main", "")
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if head != hashes[2] {
		t.Errorf("head = %s, want %s", head, hashes[2])
	}
	if len(commits) != 3 {
		t.Fatalf("got %d commits, want 3", len(commits))
	}
	for i, c := range commits {
		if c.Hash != hashes[i] {
			t.Errorf("commit %d = %s, want %s (oldest first)", i, c.Hash, hashes[i])
		}
	}
	if commits[0].Message == "" || commits[0].CommittedAt.IsZero() || commits[0].AuthorName != "t" {
		t.Errorf("metadata not populated: %+v", commits[0])
	}
}

func TestWalkSinceCursor(t *testing.T) {
	dir, hashes := buildRepo(t)
	commits, _, err := Walk(dir, "main", hashes[0])
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(commits) != 2 || commits[0].Hash != hashes[1] || commits[1].Hash != hashes[2] {
		t.Errorf("since-cursor walk wrong: %+v", commits)
	}
}

func TestWalkUnreachableCursorFallsBackToFull(t *testing.T) {
	dir, _ := buildRepo(t)
	commits, _, err := Walk(dir, "main", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(commits) != 3 {
		t.Errorf("got %d commits, want 3 (full rescan on unreachable cursor)", len(commits))
	}
}

func TestWalkUpToDate(t *testing.T) {
	dir, hashes := buildRepo(t)
	commits, head, err := Walk(dir, "main", hashes[2])
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(commits) != 0 || head != hashes[2] {
		t.Errorf("expected empty walk at head: %d commits", len(commits))
	}
}
