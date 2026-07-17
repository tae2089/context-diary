// Package store is the Postgres read model (docs/indexer-design.md §Schema).
// The database is disposable: everything here can be rebuilt from git.
package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tae2089/context-diary/internal/index"
)

// Store wraps a pgx pool.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects and pings.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

const ddl = `
CREATE TABLE IF NOT EXISTS repos (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    cursor_hash   TEXT,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS commits (
    id            BIGSERIAL PRIMARY KEY,
    repo_id       BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    hash          TEXT   NOT NULL,
    subject       TEXT   NOT NULL,
    body          TEXT   NOT NULL,
    author_name   TEXT   NOT NULL DEFAULT '',
    author_email  TEXT   NOT NULL DEFAULT '',
    committed_at  TIMESTAMPTZ NOT NULL,
    context_why   TEXT   NOT NULL,
    search        tsvector GENERATED ALWAYS AS (
                    to_tsvector('simple', subject || ' ' || context_why || ' ' || body)
                  ) STORED,
    UNIQUE (repo_id, hash)
);
CREATE INDEX IF NOT EXISTS commits_committed_at_idx ON commits (repo_id, committed_at);
CREATE INDEX IF NOT EXISTS commits_search_idx ON commits USING GIN (search);

CREATE TABLE IF NOT EXISTS commit_scopes (
    commit_id     BIGINT NOT NULL REFERENCES commits(id) ON DELETE CASCADE,
    scope         TEXT   NOT NULL,
    PRIMARY KEY (commit_id, scope)
);
CREATE INDEX IF NOT EXISTS commit_scopes_scope_idx ON commit_scopes (scope);

CREATE TABLE IF NOT EXISTS commit_details (
    commit_id     BIGINT NOT NULL REFERENCES commits(id) ON DELETE CASCADE,
    kind          TEXT   NOT NULL CHECK (kind IN ('decision', 'ref')),
    position      INT    NOT NULL,
    value         TEXT   NOT NULL,
    PRIMARY KEY (commit_id, kind, position)
);
`

// Migrate applies the idempotent DDL.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

// UpsertRepo ensures the repos row exists and returns its id and cursor.
func (s *Store) UpsertRepo(ctx context.Context, name string) (int64, string, error) {
	var id int64
	var cursor *string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO repos (name) VALUES ($1)
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, cursor_hash`, name).Scan(&id, &cursor)
	if err != nil {
		return 0, "", fmt.Errorf("upsert repo %s: %w", name, err)
	}
	if cursor == nil {
		return id, "", nil
	}
	return id, *cursor, nil
}

// SaveEntries inserts entries and moves the cursor in one transaction
// (design X17-X21). Re-runs are no-ops thanks to ON CONFLICT DO NOTHING.
func (s *Store) SaveEntries(ctx context.Context, repoID int64, entries []*index.Entry, headHash string) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	inserted := 0
	for _, e := range entries {
		var commitID int64
		err := tx.QueryRow(ctx, `
			INSERT INTO commits (repo_id, hash, subject, body, author_name, author_email, committed_at, context_why)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (repo_id, hash) DO NOTHING
			RETURNING id`,
			repoID, e.Hash, sanitize(e.Subject), sanitize(e.Message),
			sanitize(e.AuthorName), sanitize(e.AuthorEmail), e.CommittedAt, sanitize(e.Why),
		).Scan(&commitID)
		if err == pgx.ErrNoRows {
			continue // already indexed
		}
		if err != nil {
			return 0, fmt.Errorf("insert commit %s: %w", e.Hash, err)
		}
		inserted++

		for _, scope := range e.Scopes {
			if _, err := tx.Exec(ctx,
				`INSERT INTO commit_scopes (commit_id, scope) VALUES ($1, $2)`,
				commitID, scope); err != nil {
				return 0, fmt.Errorf("insert scope %s: %w", scope, err)
			}
		}
		for kind, values := range map[string][]string{"decision": e.Decisions, "ref": e.Refs} {
			for i, v := range values {
				if _, err := tx.Exec(ctx,
					`INSERT INTO commit_details (commit_id, kind, position, value) VALUES ($1, $2, $3, $4)`,
					commitID, kind, i, sanitize(v)); err != nil {
					return 0, fmt.Errorf("insert %s: %w", kind, err)
				}
			}
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE repos SET cursor_hash = $1, updated_at = now() WHERE id = $2`,
		headHash, repoID); err != nil {
		return 0, fmt.Errorf("update cursor: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return inserted, nil
}

// sanitize makes arbitrary commit bytes valid UTF-8 for Postgres TEXT (R2).
func sanitize(s string) string { return strings.ToValidUTF8(s, "�") }

// Query filters Search. Zero values mean "no filter".
type Query struct {
	Scope string
	Text  string // websearch syntax against subject+why+body
	Since time.Time
	Until time.Time
	Limit int // default 50
}

// Result is one context entry hydrated with its scopes and details.
type Result struct {
	Repo        string
	Hash        string
	Subject     string
	Why         string
	AuthorName  string
	CommittedAt time.Time
	Scopes      []string
	Decisions   []string
	Refs        []string
}

// Search returns matching entries, newest first (design §Query surface).
// Empty repoName searches across all indexed repositories.
func (s *Store) Search(ctx context.Context, repoName string, q Query) ([]Result, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	sql := `
		SELECT r.name, c.hash, c.subject, c.context_why, c.author_name, c.committed_at,
		       COALESCE((SELECT array_agg(cs.scope ORDER BY cs.scope) FROM commit_scopes cs WHERE cs.commit_id = c.id), '{}'),
		       COALESCE((SELECT array_agg(cd.value ORDER BY cd.position) FROM commit_details cd WHERE cd.commit_id = c.id AND cd.kind = 'decision'), '{}'),
		       COALESCE((SELECT array_agg(cd.value ORDER BY cd.position) FROM commit_details cd WHERE cd.commit_id = c.id AND cd.kind = 'ref'), '{}')
		FROM commits c
		JOIN repos r ON r.id = c.repo_id
		WHERE ($1 = '' OR r.name = $1)`
	args := []any{repoName}
	n := 1
	add := func(clause string, v any) {
		n++
		sql += fmt.Sprintf(" AND "+clause, n)
		args = append(args, v)
	}
	if q.Scope != "" {
		add("EXISTS (SELECT 1 FROM commit_scopes cs2 WHERE cs2.commit_id = c.id AND cs2.scope = $%d)", q.Scope)
	}
	if q.Text != "" {
		add("c.search @@ websearch_to_tsquery('simple', $%d)", q.Text)
	}
	if !q.Since.IsZero() {
		add("c.committed_at >= $%d", q.Since)
	}
	if !q.Until.IsZero() {
		add("c.committed_at <= $%d", q.Until)
	}
	n++
	sql += fmt.Sprintf(" ORDER BY c.committed_at DESC LIMIT $%d", n)
	args = append(args, q.Limit)

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()

	var out []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.Repo, &r.Hash, &r.Subject, &r.Why, &r.AuthorName, &r.CommittedAt,
			&r.Scopes, &r.Decisions, &r.Refs); err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ScopeCount is one scope with its entry count.
type ScopeCount struct {
	Scope string
	Count int
}

// ListScopes returns distinct scopes (optionally per repo) with counts.
func (s *Store) ListScopes(ctx context.Context, repoName string) ([]ScopeCount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT cs.scope, count(*)
		FROM commit_scopes cs
		JOIN commits c ON c.id = cs.commit_id
		JOIN repos r ON r.id = c.repo_id
		WHERE ($1 = '' OR r.name = $1)
		GROUP BY cs.scope ORDER BY cs.scope`, repoName)
	if err != nil {
		return nil, fmt.Errorf("list scopes: %w", err)
	}
	defer rows.Close()
	var out []ScopeCount
	for rows.Next() {
		var sc ScopeCount
		if err := rows.Scan(&sc.Scope, &sc.Count); err != nil {
			return nil, fmt.Errorf("scan scope: %w", err)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}
