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
	// canonical: owner/repo:path/to/file.go#Symbol. Requiring owner/repo keeps
	// URI values such as https://... and mailto:... out of this form. Symbol is optional.
	canonicalRe = regexp.MustCompile(`^([\w.-]+/[\w.-]+):([^#\s]+?)(?:#(\w+))?$`)
	// legacy: repo//path/to/file.go#Symbol. Keep this readable for historical
	// refs, including bare local index names; new refs should use canonicalRe.
	legacyRe = regexp.MustCompile(`^([\w.-]+(?:/[\w.-]+)?)//([^#\s]+?)(?:#(\w+))?$`)
	// GitHub blob URL: repo+path extractable; #L fragments rot and are dropped
	blobRe = regexp.MustCompile(`^https://github\.com/([\w.-]+/[\w.-]+)/blob/[^/]+/([^#\s]+)`)
)

// ParseCodeRef classifies a Context-Ref value; nil when it is a plain URL
// or issue ID rather than a code reference.
//
// @intent parse a Context-Ref into a structured code reference, or nil for a plain URL or issue id
// @domainRule canonical form is owner/repo:path#Symbol (symbol optional); legacy repo//path#Symbol remains readable; GitHub blob URLs parse to repo+path only, and #L line fragments are ignored because they rot with edits
func ParseCodeRef(ref string) *CodeRef {
	if m := blobRe.FindStringSubmatch(ref); m != nil {
		return &CodeRef{Repo: m[1], Path: m[2]}
	}
	if m := canonicalRe.FindStringSubmatch(ref); m != nil {
		return &CodeRef{Repo: m[1], Path: m[2], Symbol: m[3]}
	}
	if m := legacyRe.FindStringSubmatch(ref); m != nil {
		return &CodeRef{Repo: m[1], Path: m[2], Symbol: m[3]}
	}
	return nil
}

// GitHub line fragments (#L10) are intentionally not mapped to Symbol:
// they are positional and rot with edits, so only the canonical colon form
// can carry a symbol.
