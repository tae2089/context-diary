// Package trailer implements the Context Trailer Format v0.1
// (docs/trailer-format.md): parsing, rendering, and linting of git commit
// trailers. All functions are pure; callers own IO.
//
// @index Context trailer format: parse, render, and lint git commit trailers (Context-Why/Scope/Decision/Ref).
package trailer

import (
	"regexp"
	"strings"
)

// Canonical keys of the registry.
const (
	KeyWhy      = "Context-Why"
	KeyScope    = "Context-Scope"
	KeyDecision = "Context-Decision"
	KeyRef      = "Context-Ref"
)

// Trailer is one key/value pair from a commit's trailer block.
type Trailer struct {
	Key   string
	Value string
	// Multiline is set when the value was folded from continuation lines,
	// which the format forbids for writers (readers must still accept them).
	Multiline bool
}

var (
	trailerLineRe = regexp.MustCompile(`^([A-Za-z0-9-]+):[ \t]*(.*)$`)
	scopeRe       = regexp.MustCompile(`^[a-z0-9-]+(/[a-z0-9-]+)*$`)
	contextKeyRe  = regexp.MustCompile(`(?i)^context-[a-z0-9-]*:`)
)

// Violation codes reported by Lint.
const (
	CodeMissingWhy = "missing-why"
	CodeBadScope   = "bad-scope"
	CodeMultiline  = "multiline-value"
	CodeMisplaced  = "misplaced-trailer"
)

// Violation is one lint finding.
type Violation struct {
	Code string
	Msg  string
}

// Parse returns the trailers of msg's trailer block: the run of consecutive
// trailer-shaped paragraphs at the END of the message (more lenient than
// git, which takes only the last paragraph — GitHub's squash merge appends
// "Co-authored-by" as its own paragraph and would otherwise push the
// Context trailers into the body). A single-paragraph message has no
// trailer block (its only paragraph is the subject).
//
// @intent extract the structured trailer block from a commit or PR message
// @domainRule the trailer block is the run of consecutive all-trailer paragraphs at the end of the message — more lenient than git's last-paragraph rule, so GitHub's appended Co-authored-by paragraph does not orphan the Context trailers
// @domainRule a single-paragraph message has no trailer block; that paragraph is the subject
func Parse(msg string) []Trailer {
	block := trailingTrailerBlock(msg)
	if block == nil {
		return nil
	}
	var ts []Trailer
	for _, line := range block {
		if isContinuation(line) {
			if len(ts) == 0 {
				return nil
			}
			ts[len(ts)-1].Value += " " + strings.TrimSpace(line)
			ts[len(ts)-1].Multiline = true
			continue
		}
		m := trailerLineRe.FindStringSubmatch(line)
		if m == nil {
			return nil
		}
		ts = append(ts, Trailer{Key: m[1], Value: strings.TrimSpace(m[2])})
	}
	return ts
}

// trailingTrailerBlock returns the lines (blank separators removed) of the
// consecutive all-trailer paragraphs at the end of the message, or nil when
// the message has no such block or consists of a single paragraph.
func trailingTrailerBlock(msg string) []string {
	lines := strings.Split(strings.TrimRight(msg, "\n"), "\n")
	last := len(lines)
	for last > 0 && strings.TrimSpace(lines[last-1]) == "" {
		last--
	}
	if last == 0 {
		return nil
	}

	blockStart := last
	for {
		// paragraph boundaries
		end := blockStart
		for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		start := end
		for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
			start--
		}
		if end == 0 || start == end {
			break
		}
		if start == 0 {
			break // reached the first paragraph: that is the subject, never a trailer block
		}
		if !allTrailerShaped(lines[start:end]) {
			break
		}
		blockStart = start
	}
	if blockStart >= last {
		return nil
	}
	var block []string
	for _, l := range lines[blockStart:last] {
		if strings.TrimSpace(l) != "" {
			block = append(block, l)
		}
	}
	return block
}

func allTrailerShaped(par []string) bool {
	for i, line := range par {
		if isContinuation(line) {
			if i == 0 {
				return false
			}
			continue
		}
		if !trailerLineRe.MatchString(line) {
			return false
		}
	}
	return true
}

func isContinuation(line string) bool {
	return line != "" && (line[0] == ' ' || line[0] == '\t')
}

// HasContextWhy reports whether msg's trailer block carries a non-empty
// Context-Why value. Keys match case-insensitively.
func HasContextWhy(msg string) bool {
	for _, t := range Parse(msg) {
		if strings.EqualFold(t.Key, KeyWhy) && t.Value != "" {
			return true
		}
	}
	return false
}

// ValidScope reports whether s satisfies the scope slug grammar:
// lowercase/digit/hyphen segments separated by "/".
func ValidScope(s string) bool {
	return scopeRe.MatchString(s)
}

// StripComments removes lines starting with commentChar, so hook-injected
// draft comments are not mistaken for accepted content.
func StripComments(msg, commentChar string) string {
	var out []string
	for _, line := range strings.Split(msg, "\n") {
		if strings.HasPrefix(line, commentChar) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// Template returns empty trailer stubs for manual fill-in.
func Template() []string {
	return []string{
		KeyWhy + ": ",
		KeyScope + ": ",
	}
}

// Render formats trailers as "Key: Value" lines.
func Render(ts []Trailer) []string {
	lines := make([]string, len(ts))
	for i, t := range ts {
		lines[i] = t.Key + ": " + t.Value
	}
	return lines
}

// CommentLines prefixes each line with the comment character, producing the
// draft form a developer must uncomment to accept.
func CommentLines(lines []string, commentChar string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = commentChar + " " + l
	}
	return out
}

// Lint checks msg against the format's writer rules (design doc L4-L5):
// a non-empty Context-Why must exist, scope slugs must match the grammar,
// values must be single-line, and Context-* lines must live in the trailer
// block. Callers strip comments first when linting an editor buffer.
//
// @intent validate a commit or PR message against the trailer format and return actionable violations
// @domainRule requires a non-empty Context-Why; scope slugs must match the grammar; values must be single-line; Context-* lines must live in the trailer block
// @ensures returns an empty slice for a conforming message
func Lint(msg string) []Violation {
	var vs []Violation
	ts := Parse(msg)

	for _, line := range bodyLines(msg) {
		if contextKeyRe.MatchString(strings.TrimSpace(line)) {
			vs = append(vs, Violation{CodeMisplaced,
				"Context-* line outside the trailer block (must be the last paragraph): " + strings.TrimSpace(line)})
		}
	}

	hasWhy := false
	for _, t := range ts {
		isContext := strings.HasPrefix(strings.ToLower(t.Key), "context-")
		if strings.EqualFold(t.Key, KeyWhy) && t.Value != "" {
			hasWhy = true
		}
		if isContext && t.Multiline {
			vs = append(vs, Violation{CodeMultiline, t.Key + " value spans multiple lines; keep it on one line and move detail to the body"})
		}
		if strings.EqualFold(t.Key, KeyScope) && !ValidScope(t.Value) {
			vs = append(vs, Violation{CodeBadScope, "invalid scope slug " + strconvQuote(t.Value) + " (want lowercase segments like order/cancel)"})
		}
	}
	if !hasWhy {
		vs = append(vs, Violation{CodeMissingWhy, "no Context-Why trailer; add a one-line reason for this change"})
	}
	return vs
}

// bodyLines returns every line outside the trailing trailer block.
func bodyLines(msg string) []string {
	lines := strings.Split(strings.TrimRight(msg, "\n"), "\n")
	block := trailingTrailerBlock(msg)
	if block == nil {
		return lines
	}
	// The block's lines are the last len(block) non-blank lines; cut the
	// message at the first line of the block.
	remaining := len(block)
	cut := len(lines)
	for i := len(lines) - 1; i >= 0 && remaining > 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			remaining--
			cut = i
		}
	}
	return lines[:cut]
}

func strconvQuote(s string) string {
	return `"` + s + `"`
}
