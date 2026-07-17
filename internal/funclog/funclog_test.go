package funclog

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCommitsTouching(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
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
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "calc.go"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-q", "-b", "main")
	write("package calc\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n\nfunc Sub(a, b int) int {\n\treturn a - b\n}\n")
	git("add", ".")
	git("commit", "-q", "-m", "create Add and Sub")

	// touch only Sub — must NOT appear in Add's history
	write("package calc\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n\nfunc Sub(a, b int) int {\n\treturn a - b - 0\n}\n")
	git("add", ".")
	git("commit", "-q", "-m", "tweak Sub only")

	// touch Add
	write("package calc\n\nfunc Add(a, b int) int {\n\treturn b + a\n}\n\nfunc Sub(a, b int) int {\n\treturn a - b - 0\n}\n")
	git("add", ".")
	git("commit", "-q", "-m", "reorder Add operands")

	commits, err := CommitsTouching(dir, "main", "calc.go", "Add")
	if err != nil {
		t.Fatalf("CommitsTouching: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2 (create + reorder): %+v", len(commits), commits)
	}
	if commits[0].Subject != "create Add and Sub" || commits[1].Subject != "reorder Add operands" {
		t.Errorf("wrong order or subjects: %+v", commits)
	}
	for _, c := range commits {
		if len(c.Hash) != 40 {
			t.Errorf("bad hash %q", c.Hash)
		}
	}
}

func TestCommitsTouchingUnknownFunction(t *testing.T) {
	dir := t.TempDir()
	git := exec.Command("git", "init", "-q", "-b", "main")
	git.Dir = dir
	if err := git.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitsTouching(dir, "main", "nope.go", "Missing"); err == nil {
		t.Error("expected error for unknown file/function")
	}
}
