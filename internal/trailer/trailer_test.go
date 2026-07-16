package trailer

import (
	"reflect"
	"strings"
	"testing"
)

const specExample = `fix(order): delay refund until PG settlement is confirmed

Refunds fired immediately on cancellation caused double refunds.

Context-Why: instant refund raced with pending PG settlement, causing double refunds
Context-Scope: order/cancel
Context-Scope: payment/refund
Context-Decision: settlement-webhook trigger over PG status polling; webhook already delivers the event
Context-Ref: https://github.com/example/shop/issues/123
`

func TestParseExtractsTrailerBlock(t *testing.T) {
	got := Parse(specExample)
	want := []Trailer{
		{Key: "Context-Why", Value: "instant refund raced with pending PG settlement, causing double refunds"},
		{Key: "Context-Scope", Value: "order/cancel"},
		{Key: "Context-Scope", Value: "payment/refund"},
		{Key: "Context-Decision", Value: "settlement-webhook trigger over PG status polling; webhook already delivers the event"},
		{Key: "Context-Ref", Value: "https://github.com/example/shop/issues/123"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse() = %#v, want %#v", got, want)
	}
}

func TestParseNoTrailerBlock(t *testing.T) {
	cases := map[string]string{
		"subject only":            "fix: something\n",
		"subject plus plain body": "fix: something\n\njust prose, no trailers here.\n",
		"empty message":           "",
		"single paragraph that looks like a trailer": "Context-Why: subject line is not a trailer block\n",
		"last paragraph mixes prose and trailers":    "fix: x\n\nContext-Why: y\nbut this line is prose\n",
	}
	for name, msg := range cases {
		if got := Parse(msg); len(got) != 0 {
			t.Errorf("%s: Parse() = %#v, want empty", name, got)
		}
	}
}

func TestParseKeepsUnknownAndForeignKeys(t *testing.T) {
	msg := "fix: x\n\nContext-Why: reason\nContext-Future: something new\nSigned-off-by: A <a@b.c>\n"
	got := Parse(msg)
	if len(got) != 3 {
		t.Fatalf("Parse() returned %d trailers, want 3: %#v", len(got), got)
	}
	if got[1].Key != "Context-Future" || got[2].Key != "Signed-off-by" {
		t.Errorf("unexpected keys: %#v", got)
	}
}

func TestParseFoldsContinuationLines(t *testing.T) {
	msg := "fix: x\n\nContext-Why: first part\n  second part\n"
	got := Parse(msg)
	if len(got) != 1 {
		t.Fatalf("Parse() = %#v, want 1 trailer", got)
	}
	if got[0].Value != "first part second part" {
		t.Errorf("folded value = %q", got[0].Value)
	}
	if !got[0].Multiline {
		t.Error("Multiline flag not set on folded trailer")
	}
}

func TestHasContextWhy(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"present", specExample, true},
		{"absent", "fix: x\n\nbody\n", false},
		{"case-insensitive key", "fix: x\n\ncontext-why: lowered\n", true},
		{"empty value is not present", "fix: x\n\nContext-Why:\nContext-Scope: a\n", false},
		{"whitespace value is not present", "fix: x\n\nContext-Why:   \nContext-Scope: a\n", false},
	}
	for _, c := range cases {
		if got := HasContextWhy(c.msg); got != c.want {
			t.Errorf("%s: HasContextWhy = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestValidScope(t *testing.T) {
	valid := []string{"auth", "order/cancel", "payment/refund-v2", "a/b/c", "a1/b2"}
	invalid := []string{"", "Order/Cancel", "order cancel", "order//cancel", "/order", "order/", "ordér"}
	for _, s := range valid {
		if !ValidScope(s) {
			t.Errorf("ValidScope(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if ValidScope(s) {
			t.Errorf("ValidScope(%q) = true, want false", s)
		}
	}
}

func TestStripComments(t *testing.T) {
	msg := "fix: x\n\n# Context-Why: draft not yet accepted\nreal body\n"
	got := StripComments(msg, "#")
	if strings.Contains(got, "draft") {
		t.Errorf("comment line survived: %q", got)
	}
	if !strings.Contains(got, "real body") {
		t.Errorf("non-comment line lost: %q", got)
	}
}

func TestTemplateAndRender(t *testing.T) {
	tmpl := Template()
	joined := strings.Join(tmpl, "\n")
	if !strings.Contains(joined, "Context-Why: ") || !strings.Contains(joined, "Context-Scope: ") {
		t.Errorf("Template() missing stubs: %#v", tmpl)
	}

	lines := Render([]Trailer{{Key: "Context-Why", Value: "because"}})
	if !reflect.DeepEqual(lines, []string{"Context-Why: because"}) {
		t.Errorf("Render() = %#v", lines)
	}

	commented := CommentLines([]string{"Context-Why: because"}, "#")
	if !reflect.DeepEqual(commented, []string{"# Context-Why: because"}) {
		t.Errorf("CommentLines() = %#v", commented)
	}
}

func TestLint(t *testing.T) {
	cases := []struct {
		name      string
		msg       string
		wantCodes []string
	}{
		{"clean", specExample, nil},
		{"missing why", "fix: x\n\nContext-Scope: auth\n", []string{CodeMissingWhy}},
		{"empty why", "fix: x\n\nContext-Why:\nContext-Scope: auth\n", []string{CodeMissingWhy}},
		{"bad scope", "fix: x\n\nContext-Why: y\nContext-Scope: Bad Scope\n", []string{CodeBadScope}},
		{"multiline value", "fix: x\n\nContext-Why: a\n  continued\n", []string{CodeMultiline}},
		{"misplaced trailer in body", "fix: x\n\nContext-Why: placed in body\n\nSigned-off-by: A <a@b.c>\n", []string{CodeMisplaced, CodeMissingWhy}},
		{"no trailers at all", "fix: x\n\nplain body\n", []string{CodeMissingWhy}},
	}
	for _, c := range cases {
		vs := Lint(c.msg)
		var codes []string
		for _, v := range vs {
			codes = append(codes, v.Code)
		}
		if !reflect.DeepEqual(codes, c.wantCodes) {
			t.Errorf("%s: Lint codes = %v, want %v (violations: %#v)", c.name, codes, c.wantCodes, vs)
		}
	}
}
