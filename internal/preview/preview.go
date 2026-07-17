// Package preview renders the bot's single PR comment
// (docs/serve-design.md W4-W5): an index preview when the PR description is
// clean, or violations plus a fill-in template when it is not. Pure.
package preview

import (
	"fmt"
	"strings"

	"github.com/tae2089/context-diary/internal/trailer"
)

// Marker identifies the bot's comment for idempotent upserts.
const Marker = "<!-- context-diary -->"

// Comment renders the bot comment for a PR description. The body is linted
// the way GitHub's "PR title & description" squash setting will compose the
// commit message (synthetic subject + body).
func Comment(prBody string) string {
	msg := "subject\n\n" + prBody
	vs := trailer.Lint(msg)

	var b strings.Builder
	b.WriteString(Marker + "\n")
	if len(vs) == 0 {
		b.WriteString("### Context check ✅\n\nOn merge, this PR will be indexed as:\n\n")
		for _, t := range trailer.Parse(msg) {
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
		return b.String()
	}

	b.WriteString("### Context check ❌\n\nThis PR description is missing context trailers:\n\n")
	for _, v := range vs {
		fmt.Fprintf(&b, "- `%s`: %s\n", v.Code, v.Msg)
	}
	b.WriteString("\nAdd this to the **end** of the PR description and fill it in:\n\n```\n")
	for _, line := range trailer.Template() {
		b.WriteString(line + "\n")
	}
	b.WriteString("```\n\nWith the \"squash and merge (PR title & description)\" setting, these trailers become part of the commit message and are indexed by context-diary.\n")
	return b.String()
}
