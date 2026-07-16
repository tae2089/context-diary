// Package index maps git commits to context entries per
// docs/indexer-design.md. Pure: no IO, no database.
package index

import (
	"strings"
	"time"

	"github.com/tae2089/context-diary/internal/trailer"
)

// Commit is the raw material from the history walk.
type Commit struct {
	Hash        string
	Message     string
	AuthorName  string
	AuthorEmail string
	CommittedAt time.Time
}

// Entry is one indexable context record.
type Entry struct {
	Hash        string
	Subject     string
	Message     string
	AuthorName  string
	AuthorEmail string
	CommittedAt time.Time

	Why       string
	Scopes    []string
	Decisions []string
	Refs      []string
	// DroppedScopes records invalid slugs for operator visibility.
	DroppedScopes []string
}

// EntryFromCommit parses the commit message per the trailer format. It
// returns nil when the commit carries no non-empty Context-Why — such
// commits are simply not indexed (spec) — and never fails.
func EntryFromCommit(c Commit) *Entry {
	e := &Entry{
		Hash:        c.Hash,
		Subject:     firstLine(c.Message),
		Message:     c.Message,
		AuthorName:  c.AuthorName,
		AuthorEmail: c.AuthorEmail,
		CommittedAt: c.CommittedAt,
	}
	seen := map[string]bool{}
	for _, t := range trailer.Parse(c.Message) {
		switch {
		case strings.EqualFold(t.Key, trailer.KeyWhy):
			if e.Why == "" && t.Value != "" {
				e.Why = t.Value
			}
		case strings.EqualFold(t.Key, trailer.KeyScope):
			switch {
			case !trailer.ValidScope(t.Value):
				e.DroppedScopes = append(e.DroppedScopes, t.Value)
			case !seen[t.Value]:
				seen[t.Value] = true
				e.Scopes = append(e.Scopes, t.Value)
			}
		case strings.EqualFold(t.Key, trailer.KeyDecision):
			if t.Value != "" {
				e.Decisions = append(e.Decisions, t.Value)
			}
		case strings.EqualFold(t.Key, trailer.KeyRef):
			if t.Value != "" {
				e.Refs = append(e.Refs, t.Value)
			}
		}
	}
	if e.Why == "" {
		return nil
	}
	return e
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
