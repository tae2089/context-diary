package preview

import (
	"strings"
	"testing"
)

const cleanBody = "Fixes the refund race.\n\nContext-Why: refund raced with settlement\nContext-Scope: order/cancel\nContext-Decision: webhook over polling; simpler\n"

func cleanCommit(sha string) Commit {
	return Commit{SHA: sha, Message: "feat: x\n\nContext-Why: commit-level reason for " + sha + "\nContext-Scope: order/cancel\n"}
}

func dirtyCommit(sha string) Commit {
	return Commit{SHA: sha, Message: "wip: stuff\n\nno trailers\n"}
}

func TestEvaluateBodyPath(t *testing.T) {
	res := Evaluate(cleanBody, []Commit{dirtyCommit("aaa1111")})
	if !res.Pass {
		t.Fatal("clean body must pass regardless of commits")
	}
	for _, want := range []string{Marker, "✅", "refund raced with settlement", "`order/cancel`"} {
		if !strings.Contains(res.Comment, want) {
			t.Errorf("comment missing %q:\n%s", want, res.Comment)
		}
	}
	if !strings.Contains(res.Desc, "PR description") {
		t.Errorf("desc should name the path: %q", res.Desc)
	}
}

func TestEvaluateCommitPath(t *testing.T) {
	commits := []Commit{
		cleanCommit("aaa1111bbb"),
		{SHA: "mmm999", Message: "Merge branch 'x'", Merge: true}, // merge commits exempt
		cleanCommit("ccc2222ddd"),
	}
	res := Evaluate("just prose, no trailers", commits)
	if !res.Pass {
		t.Fatalf("all-clean commits must pass: %+v", res)
	}
	for _, want := range []string{"✅", "aaa1111", "ccc2222", "commit-level reason"} {
		if !strings.Contains(res.Comment, want) {
			t.Errorf("comment missing %q:\n%s", want, res.Comment)
		}
	}
	if !strings.Contains(res.Comment, "squash") {
		t.Error("commit-path comment must warn that squash discards commit messages")
	}
	if !strings.Contains(res.Desc, "commit") {
		t.Errorf("desc should name the path: %q", res.Desc)
	}
}

func TestEvaluateBothDirty(t *testing.T) {
	res := Evaluate("no trailers here", []Commit{cleanCommit("aaa1111"), dirtyCommit("bbb2222ccc")})
	if res.Pass {
		t.Fatal("must fail when body dirty and a commit lacks context")
	}
	for _, want := range []string{"❌", "missing-why", "Context-Why: ", "bbb2222"} {
		if !strings.Contains(res.Comment, want) {
			t.Errorf("comment missing %q:\n%s", want, res.Comment)
		}
	}
	if strings.Contains(res.Comment, "aaa1111 ") && strings.Contains(res.Comment, "lack context: aaa1111") {
		t.Error("clean commit listed as offender")
	}
	if len(res.Detail) == 0 {
		t.Error("detail lines empty")
	}
}

func TestEvaluateNoCommitsDirtyBody(t *testing.T) {
	res := Evaluate("nothing", nil)
	if res.Pass {
		t.Fatal("no commits + dirty body must fail")
	}
}
