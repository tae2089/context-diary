package index

import "testing"

func TestParseCodeRef(t *testing.T) {
	cases := []struct {
		in   string
		want *CodeRef // nil = not a code ref
	}{
		{"acme/shop//internal/refund/refund.go#ProcessRefund",
			&CodeRef{Repo: "acme/shop", Path: "internal/refund/refund.go", Symbol: "ProcessRefund"}},
		{"acme/shop//internal/refund/refund.go",
			&CodeRef{Repo: "acme/shop", Path: "internal/refund/refund.go"}},
		{"context-diary//internal/store/store.go#SaveEntries",
			&CodeRef{Repo: "context-diary", Path: "internal/store/store.go", Symbol: "SaveEntries"}},
		{"https://github.com/acme/shop/blob/main/internal/refund/refund.go",
			&CodeRef{Repo: "acme/shop", Path: "internal/refund/refund.go"}},
		{"https://github.com/acme/shop/blob/abc123def/pkg/a.go#L10",
			&CodeRef{Repo: "acme/shop", Path: "pkg/a.go"}}, // line fragments rot; dropped
		{"https://example.com/docs/design.md", nil}, // plain URL
		{"JIRA-123", nil},           // issue id
		{"docs/cli-design.md", nil}, // single slash, no // boundary
		{"https://github.com/acme/shop/issues/5", nil},
		{"https://github.com/acme/shop/pull/7", nil},
	}
	for _, c := range cases {
		got := ParseCodeRef(c.in)
		switch {
		case c.want == nil && got != nil:
			t.Errorf("ParseCodeRef(%q) = %+v, want nil", c.in, got)
		case c.want != nil && got == nil:
			t.Errorf("ParseCodeRef(%q) = nil, want %+v", c.in, c.want)
		case c.want != nil && got != nil && *got != *c.want:
			t.Errorf("ParseCodeRef(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestEntryCollectsCodeRefs(t *testing.T) {
	msg := `fix: x

Context-Why: y
Context-Ref: https://example.com/postmortem
Context-Ref: acme/shop//internal/refund/refund.go#ProcessRefund
`
	e := EntryFromCommit(meta(msg))
	if e == nil {
		t.Fatal("nil entry")
	}
	if len(e.Refs) != 2 {
		t.Errorf("raw refs = %v (both forms must stay searchable text)", e.Refs)
	}
	if len(e.CodeRefs) != 1 || e.CodeRefs[0].Symbol != "ProcessRefund" {
		t.Errorf("code refs = %+v", e.CodeRefs)
	}
}
