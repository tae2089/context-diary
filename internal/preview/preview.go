// Package preview evaluates a PR's context coverage and renders the bot's
// single comment (docs/serve-design.md W4-W5). Two passing paths:
//
//   - body path (squash teams): the PR description carries trailers — the
//     "PR title & description" squash setting lands them in git;
//   - commit path (merge/rebase teams): every non-merge branch commit
//     carries trailers — they land on main individually.
//
// Pure: no IO.
package preview

import (
	"fmt"
	"strings"

	"github.com/tae2089/context-diary/internal/trailer"
)

// Marker identifies the bot's comment for idempotent upserts.
const Marker = "<!-- context-diary -->"

// Commit is one PR branch commit under evaluation.
type Commit struct {
	SHA     string
	Message string
	Merge   bool // merge commits are exempt (they are stitches, not changes)
}

// Result is the evaluation outcome for the comment, status, and check page.
type Result struct {
	Pass    bool
	Desc    string   // short commit-status description
	Comment string   // bot comment markdown
	Detail  []string // check detail page lines
}

// Evaluate lints both paths and renders the verdict.
func Evaluate(prBody string, commits []Commit) Result {
	bodyMsg := "subject\n\n" + prBody
	bodyVs := trailer.Lint(bodyMsg)
	bodyClean := len(bodyVs) == 0

	type commitState struct {
		sha, subject, why string
		clean             bool
	}
	var states []commitState
	commitsClean := true
	checked := 0
	for _, c := range commits {
		if c.Merge {
			continue
		}
		checked++
		clean := len(trailer.Lint(c.Message)) == 0
		why := ""
		for _, t := range trailer.Parse(c.Message) {
			if strings.EqualFold(t.Key, trailer.KeyWhy) && t.Value != "" {
				why = t.Value
				break
			}
		}
		states = append(states, commitState{
			sha:     short(c.SHA),
			subject: firstLine(c.Message),
			why:     why,
			clean:   clean,
		})
		if !clean {
			commitsClean = false
		}
	}
	commitPath := checked > 0 && commitsClean

	var b strings.Builder
	b.WriteString(Marker + "\n")

	switch {
	case bodyClean:
		b.WriteString("### Context check ✅ (PR description)\n\nOn squash merge, this PR will be indexed as:\n\n")
		for _, t := range trailer.Parse(bodyMsg) {
			switch {
			case strings.EqualFold(t.Key, trailer.KeyWhy):
				fmt.Fprintf(&b, "- **Why:** %s\n", t.Value)
			case strings.EqualFold(t.Key, trailer.KeyScope):
				fmt.Fprintf(&b, "- **Scope:** `%s`\n", t.Value)
			case strings.EqualFold(t.Key, trailer.KeyDecision):
				fmt.Fprintf(&b, "- **Decision:** %s\n", t.Value)
			case strings.EqualFold(t.Key, trailer.KeyRef):
				fmt.Fprintf(&b, "- **Ref:** %s\n", t.Value)
			}
		}
		return Result{
			Pass:    true,
			Desc:    "context trailers present (PR description)",
			Comment: b.String(),
			Detail:  detailFromComment(b.String()),
		}

	case commitPath:
		fmt.Fprintf(&b, "### Context check ✅ (branch commits)\n\nAll %d non-merge commits carry context and will be indexed individually on a merge or rebase merge:\n\n", checked)
		for _, s := range states {
			fmt.Fprintf(&b, "- `%s` %s\n  - why: %s\n", s.sha, s.subject, s.why)
		}
		b.WriteString("\n⚠️ A **squash** merge discards these commit messages — if this repo squashes, add the trailers to the PR description too.\n")
		return Result{
			Pass:    true,
			Desc:    fmt.Sprintf("context present on all %d commits", checked),
			Comment: b.String(),
			Detail:  detailFromComment(b.String()),
		}

	default:
		b.WriteString("### Context check ❌\n\nNeither path carries context:\n\n**PR description** is missing trailers:\n")
		for _, v := range bodyVs {
			fmt.Fprintf(&b, "- `%s`: %s\n", v.Code, v.Msg)
		}
		if checked == 0 {
			b.WriteString("\n**Branch commits**: none to check.\n")
		} else {
			b.WriteString("\n**Branch commits** that lack context:\n")
			for _, s := range states {
				if !s.clean {
					fmt.Fprintf(&b, "- `%s` %s\n", s.sha, s.subject)
				}
			}
		}
		b.WriteString("\nFix EITHER path — add this to the **end** of the PR description (squash teams):\n\n```\n")
		for _, line := range trailer.Template() {
			b.WriteString(line + "\n")
		}
		b.WriteString("```\n\nor amend the listed commits with trailers (merge/rebase teams).\n")
		return Result{
			Pass:    false,
			Desc:    "missing context trailers (see Details)",
			Comment: b.String(),
			Detail:  detailFromComment(b.String()),
		}
	}
}

// detailFromComment reuses the comment content for the check page, minus
// the HTML marker.
func detailFromComment(md string) []string {
	lines := strings.Split(strings.TrimSpace(md), "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "<!--") {
		lines = lines[1:]
	}
	return lines
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
