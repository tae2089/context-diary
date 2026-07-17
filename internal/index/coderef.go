package index

import "regexp"

// CodeRef is a Context-Ref that points at code in some repository —
// the structured join key for cross-repo questions (docs/trailer-format.md
// §Ref forms).
type CodeRef struct {
	Repo   string // "owner/repo"
	Path   string // file path within that repo
	Symbol string // optional function/method name
}

var (
	// canonical: repo//path/to/file.go#Symbol — repo is "owner/name" or a
	// bare local index name; the literal "//" is the boundary. Symbol optional.
	compactRe = regexp.MustCompile(`^([\w.-]+(?:/[\w.-]+)?)//([^#\s]+?)(?:#(\w+))?$`)
	// GitHub blob URL: repo+path extractable; #L fragments rot and are dropped
	blobRe = regexp.MustCompile(`^https://github\.com/([\w.-]+/[\w.-]+)/blob/[^/]+/([^#\s]+)`)
)

// ParseCodeRef classifies a Context-Ref value; nil when it is a plain URL
// or issue ID rather than a code reference.
func ParseCodeRef(ref string) *CodeRef {
	if m := compactRe.FindStringSubmatch(ref); m != nil {
		return &CodeRef{Repo: m[1], Path: m[2], Symbol: m[3]}
	}
	if m := blobRe.FindStringSubmatch(ref); m != nil {
		return &CodeRef{Repo: m[1], Path: m[2]}
	}
	return nil
}

// GitHub line fragments (#L10) are intentionally not mapped to Symbol:
// they are positional and rot with edits, so only the canonical compact
// form can carry a symbol.
