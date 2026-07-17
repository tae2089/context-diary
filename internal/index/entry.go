// Package index maps git commits to context entries per
// docs/indexer-design.md. Pure: no IO, no database.
package index

import (
	"regexp"
	"strings"
	"time"

	"github.com/tae2089/context-diary/internal/trailer"
)

// Commit is the raw material from the history walk.
type Commit struct {
	Hash        string
	Message     string
	Note        string // refs/notes/context-diary content, if any (backfill)
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
// returns nil when neither the commit message nor its backfill note carries
// a non-empty Context-Why — such commits are simply not indexed (spec) —
// and never fails.
//
// Precedence: authored commit trailers win entirely; the git note
// (docs/backfill.md) is consulted only when the message has no Context-Why.
func EntryFromCommit(c Commit) *Entry {
	trailers := trailer.Parse(c.Message)
	message := c.Message
	if !hasWhy(trailers) && c.Note != "" {
		trailers = noteTrailers(c.Note)
		// Fold the note into the stored message: it becomes searchable and
		// note edits register as content changes on re-index.
		message = c.Message + "\n[context-diary note]\n" + c.Note
	}

	e := &Entry{
		Hash:        c.Hash,
		Subject:     firstLine(c.Message),
		Message:     message,
		AuthorName:  c.AuthorName,
		AuthorEmail: c.AuthorEmail,
		CommittedAt: c.CommittedAt,
	}
	seen := map[string]bool{}
	for _, t := range trailers {
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

func hasWhy(ts []trailer.Trailer) bool {
	for _, t := range ts {
		if strings.EqualFold(t.Key, trailer.KeyWhy) && t.Value != "" {
			return true
		}
	}
	return false
}

var noteLineRe = regexp.MustCompile(`^([A-Za-z0-9-]+):[ \t]*(.+)$`)

// noteTrailers parses a backfill note line-wise: a note IS a trailer block
// by definition, so the message's last-paragraph rule does not apply.
func noteTrailers(note string) []trailer.Trailer {
	var ts []trailer.Trailer
	for _, line := range strings.Split(note, "\n") {
		if m := noteLineRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			ts = append(ts, trailer.Trailer{Key: m[1], Value: strings.TrimSpace(m[2])})
		}
	}
	return ts
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
