package preview

import (
	"strings"
	"testing"
)

func TestCommentForCleanBody(t *testing.T) {
	body := "Fixes the refund race.\n\nContext-Why: refund raced with settlement\nContext-Scope: order/cancel\nContext-Decision: webhook over polling; simpler\n"
	got := Comment(body)
	if !strings.Contains(got, Marker) {
		t.Error("comment missing marker")
	}
	for _, want := range []string{"✅", "refund raced with settlement", "`order/cancel`", "webhook over polling"} {
		if !strings.Contains(got, want) {
			t.Errorf("clean comment missing %q:\n%s", want, got)
		}
	}
}

func TestCommentForViolatingBody(t *testing.T) {
	got := Comment("Just prose, no trailers.\n")
	if !strings.Contains(got, Marker) {
		t.Error("comment missing marker")
	}
	for _, want := range []string{"❌", "missing-why", "Context-Why: ", "Context-Scope: "} {
		if !strings.Contains(got, want) {
			t.Errorf("violation comment missing %q:\n%s", want, got)
		}
	}
}
