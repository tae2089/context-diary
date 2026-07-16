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

// buildMergeRepo creates: base -> (side branch commit) -> merge into main.
// Returns path and hashes: [base, side, merge].
func buildMergeRepo(t *testing.T) (string, []string) {
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
	rev := func() string { return string([]byte(git("rev-parse", "HEAD"))[:40]) }

	git("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "base"), []byte("base"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "base\n\nContext-Why: base reason\n")
	base := rev()

	git("checkout", "-q", "-b", "feature")
	os.WriteFile(filepath.Join(dir, "side"), []byte("side"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "side work\n\nContext-Why: side reason\n")
	side := rev()

	git("checkout", "-q", "main")
	git("merge", "-q", "--no-ff", "-m", "Merge branch 'feature'", "feature")
	merge := rev()
	return dir, []string{base, side, merge}
}

func TestWalkFirstParentMissesSideBranch(t *testing.T) {
	dir, hashes := buildMergeRepo(t)
	commits, _, err := Walk(dir, "main", "")
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	for _, c := range commits {
		if c.Hash == hashes[1] {
			t.Error("first-parent walk should not include the side-branch commit")
		}
	}
	if len(commits) != 2 {
		t.Errorf("first-parent walk = %d commits, want 2 (base, merge)", len(commits))
	}
}

func TestWalkFullIncludesSideBranch(t *testing.T) {
	dir, hashes := buildMergeRepo(t)
	commits, head, err := WalkFull(dir, "main", "")
	if err != nil {
		t.Fatalf("WalkFull: %v", err)
	}
	if head != hashes[2] {
		t.Errorf("head = %s, want merge %s", head, hashes[2])
	}
	got := map[string]bool{}
	for _, c := range commits {
		got[c.Hash] = true
	}
	for i, h := range hashes {
		if !got[h] {
			t.Errorf("full walk missing commit %d (%s)", i, h)
		}
	}
	if len(commits) != 3 {
		t.Errorf("full walk = %d commits, want 3", len(commits))
	}
	// oldest first by committer time
	if commits[0].Hash != hashes[0] {
		t.Errorf("first commit = %s, want base %s", commits[0].Hash, hashes[0])
	}
}

func TestWalkFullIncremental(t *testing.T) {
	dir, hashes := buildMergeRepo(t)
	// cursor at base: only side + merge are new
	commits, _, err := WalkFull(dir, "main", hashes[0])
	if err != nil {
		t.Fatalf("WalkFull: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("incremental full walk = %d commits, want 2: %+v", len(commits), commits)
	}
	got := map[string]bool{commits[0].Hash: true, commits[1].Hash: true}
	if !got[hashes[1]] || !got[hashes[2]] {
		t.Errorf("incremental full walk wrong set: %v", got)
	}

	// cursor at head: nothing new
	commits, _, err = WalkFull(dir, "main", hashes[2])
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 0 {
		t.Errorf("up-to-date full walk = %d commits, want 0", len(commits))
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
