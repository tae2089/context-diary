package mirror

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tae2089/context-diary/internal/gitlog"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.local",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.local",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestSyncCloneThenFetch(t *testing.T) {
	src := t.TempDir()
	gitIn(t, src, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(src, "a"), []byte("a"), 0o644)
	gitIn(t, src, "add", ".")
	gitIn(t, src, "commit", "-q", "-m", "one\n\nContext-Why: first\n")

	cache := t.TempDir()

	// first sync = clone
	path, err := Sync(cache, "acme/shop", src, "")
	if err != nil {
		t.Fatalf("Sync (clone): %v", err)
	}
	commits, _, err := gitlog.Walk(path, "main", "")
	if err != nil {
		t.Fatalf("Walk on mirror: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("mirror has %d commits, want 1", len(commits))
	}

	// new commit upstream, second sync = fetch
	os.WriteFile(filepath.Join(src, "b"), []byte("b"), 0o644)
	gitIn(t, src, "add", ".")
	gitIn(t, src, "commit", "-q", "-m", "two\n\nContext-Why: second\n")

	path2, err := Sync(cache, "acme/shop", src, "")
	if err != nil {
		t.Fatalf("Sync (fetch): %v", err)
	}
	if path2 != path {
		t.Errorf("path changed across syncs: %s vs %s", path, path2)
	}
	commits, _, err = gitlog.Walk(path, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Errorf("mirror has %d commits after fetch, want 2", len(commits))
	}
}
