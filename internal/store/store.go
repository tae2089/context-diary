// Package store is the Postgres read model (docs/indexer-design.md §Schema).
// The database is disposable: everything here can be rebuilt from git.
//
// @index Postgres read model of the context index: schema, upsert-on-change ingestion, and scope/time/text/code-ref queries.
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

const ddl = `
CREATE EXTENSION IF NOT EXISTS pg_trgm;

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
-- Trigram index: substring matching for agglutinative languages (Korean
-- particles defeat the 'simple' FTS dictionary).
CREATE INDEX IF NOT EXISTS commits_trgm_idx ON commits
    USING GIN ((subject || ' ' || context_why || ' ' || body) gin_trgm_ops);

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

-- Structured code references parsed from Context-Ref values
-- (docs/trailer-format.md §Ref forms): the cross-repo join surface.
CREATE TABLE IF NOT EXISTS commit_code_refs (
    commit_id     BIGINT NOT NULL REFERENCES commits(id) ON DELETE CASCADE,
    ref_repo      TEXT   NOT NULL,
    ref_path      TEXT   NOT NULL,
    ref_symbol    TEXT   NOT NULL DEFAULT '',
    PRIMARY KEY (commit_id, ref_repo, ref_path, ref_symbol)
);
CREATE INDEX IF NOT EXISTS commit_code_refs_target_idx ON commit_code_refs (ref_repo, ref_path);
`

// Query filters Search. Zero values mean "no filter".
type Query struct {
	Scope  string
	Text   string // websearch syntax against subject+why+body
	Since  time.Time
	Until  time.Time
	Limit  int // default 50
	Offset int // rows to skip (pagination)
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

// ScopeCount is one scope with its entry count.
type ScopeCount struct {
	Scope string
	Count int
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

// Migrate applies the idempotent DDL.
//
// @intent create or update the schema on startup without a migration framework
// @domainRule DDL is CREATE ... IF NOT EXISTS only; no migration tool until the first breaking schema change (YAGNI)
// @sideEffect creates the pg_trgm extension, tables, and indexes in Postgres
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
// (design X17-X21). Identical re-runs are no-ops; force rebuilds every row's
// derived children even when the commit content is unchanged (needed when
// the PARSER improves — e.g. new Context-Ref forms — since change detection
// is content-based).
//
// @intent atomically persist a batch of context entries and advance the repo cursor so a crash never skips or duplicates commits
// @domainRule upsert-on-change: an unchanged commit is a no-op; changed content (e.g. an edited backfill note) rebuilds its scopes/details/code-refs
// @domainRule force=true rebuilds derived children even when content is unchanged, so parser upgrades reach already-indexed commits (used by --rescan)
// @sideEffect writes commits, commit_scopes, commit_details, commit_code_refs and the repos cursor in one Postgres transaction
// @ensures on error the transaction rolls back leaving no partial state
// @return the number of inserted or refreshed commits
func (s *Store) SaveEntries(ctx context.Context, repoID int64, entries []*index.Entry, headHash string, force bool) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	inserted := 0
	for _, e := range entries {
		var commitID int64
		// Upsert-on-change: RETURNING fires only for a new row or when the
		// content actually differs (e.g. an edited backfill note), so
		// identical re-runs stay no-ops and children are rebuilt only when
		// needed.
		err := tx.QueryRow(ctx, `
			INSERT INTO commits (repo_id, hash, subject, body, author_name, author_email, committed_at, context_why)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (repo_id, hash) DO UPDATE SET
				subject = EXCLUDED.subject,
				body = EXCLUDED.body,
				context_why = EXCLUDED.context_why
			WHERE $9
			   OR commits.subject IS DISTINCT FROM EXCLUDED.subject
			   OR commits.body IS DISTINCT FROM EXCLUDED.body
			   OR commits.context_why IS DISTINCT FROM EXCLUDED.context_why
			RETURNING id`,
			repoID, e.Hash, sanitize(e.Subject), sanitize(e.Message),
			sanitize(e.AuthorName), sanitize(e.AuthorEmail), e.CommittedAt, sanitize(e.Why), force,
		).Scan(&commitID)
		if err == pgx.ErrNoRows {
			continue // already indexed with identical content
		}
		if err != nil {
			return 0, fmt.Errorf("insert commit %s: %w", e.Hash, err)
		}
		inserted++

		if _, err := tx.Exec(ctx,
			`DELETE FROM commit_scopes WHERE commit_id = $1`, commitID); err != nil {
			return 0, fmt.Errorf("clear scopes: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM commit_details WHERE commit_id = $1`, commitID); err != nil {
			return 0, fmt.Errorf("clear details: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM commit_code_refs WHERE commit_id = $1`, commitID); err != nil {
			return 0, fmt.Errorf("clear code refs: %w", err)
		}

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
		for _, cr := range e.CodeRefs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO commit_code_refs (commit_id, ref_repo, ref_path, ref_symbol)
				VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
				commitID, cr.Repo, cr.Path, cr.Symbol); err != nil {
				return 0, fmt.Errorf("insert code ref: %w", err)
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

// Search returns matching entries, newest first (design §Query surface).
// Empty repoName searches across all indexed repositories.
//
// @intent answer "why did this area change" by scope, time window, and free text — the non-developer query entry point
// @domainRule free-text matches when EITHER the tsvector FTS query OR an all-words trigram substring hits, so agglutinative-language (Korean) stems find their inflected forms
// @domainRule an empty repoName searches across every indexed repository
func (s *Store) Search(ctx context.Context, repoName string, q Query) ([]Result, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	sql := resultSelect + `
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
		// FTS for token languages OR an all-words substring match for
		// agglutinative ones (trigram-indexed ILIKE).
		n++
		args = append(args, q.Text)
		clause := fmt.Sprintf("(c.search @@ websearch_to_tsquery('simple', $%d) OR (true", n)
		for _, word := range strings.Fields(q.Text) {
			n++
			clause += fmt.Sprintf(" AND (subject || ' ' || context_why || ' ' || body) ILIKE $%d", n)
			args = append(args, "%"+word+"%")
		}
		sql += " AND " + clause + "))"
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
	if q.Offset > 0 {
		n++
		sql += fmt.Sprintf(" OFFSET $%d", n)
		args = append(args, q.Offset)
	}

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	return scanResults(rows)
}

// ByHashes hydrates index entries for the given commit hashes, oldest
// first. Hashes with no entry (no context) are simply absent from the
// result. Empty repoName matches any repository.
func (s *Store) ByHashes(ctx context.Context, repoName string, hashes []string) ([]Result, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, resultSelect+`
		WHERE ($1 = '' OR r.name = $1) AND c.hash = ANY($2)
		ORDER BY c.committed_at ASC`, repoName, hashes)
	if err != nil {
		return nil, fmt.Errorf("by hashes: %w", err)
	}
	return scanResults(rows)
}

// resultSelect is the shared projection for entry queries; append a WHERE
// clause referencing commits c and repos r.
const resultSelect = `
	SELECT r.name, c.hash, c.subject, c.context_why, c.author_name, c.committed_at,
	       COALESCE((SELECT array_agg(cs.scope ORDER BY cs.scope) FROM commit_scopes cs WHERE cs.commit_id = c.id), '{}'),
	       COALESCE((SELECT array_agg(cd.value ORDER BY cd.position) FROM commit_details cd WHERE cd.commit_id = c.id AND cd.kind = 'decision'), '{}'),
	       COALESCE((SELECT array_agg(cd.value ORDER BY cd.position) FROM commit_details cd WHERE cd.commit_id = c.id AND cd.kind = 'ref'), '{}')
	FROM commits c
	JOIN repos r ON r.id = c.repo_id`

func scanResults(rows pgx.Rows) ([]Result, error) {
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

// ReferencedBy returns entries in ANY repository whose code refs point at
// the given repo+path (and symbol, when the ref carries one; path-level
// refs match every symbol in the file). Oldest first.
//
// @intent reverse-lookup cross-repo impact: "which decision in another service concerns this function"
// @domainRule a path-level code ref (empty symbol) matches every symbol in that file
func (s *Store) ReferencedBy(ctx context.Context, refRepo, refPath, symbol string) ([]Result, error) {
	rows, err := s.pool.Query(ctx, resultSelect+`
		WHERE EXISTS (
			SELECT 1 FROM commit_code_refs ccr
			WHERE ccr.commit_id = c.id
			  AND ccr.ref_repo = $1 AND ccr.ref_path = $2
			  AND (ccr.ref_symbol = $3 OR ccr.ref_symbol = '')
		)
		ORDER BY c.committed_at ASC`, refRepo, refPath, symbol)
	if err != nil {
		return nil, fmt.Errorf("referenced by: %w", err)
	}
	return scanResults(rows)
}

// ByRefText returns entries across all repositories whose Context-Ref
// values contain the query text (Jira keys, doc URLs, postmortems — the
// cross-repo join for non-code refs). Oldest first.
//
// @intent answer "which repos did this ticket or incident touch" by joining on a shared Context-Ref value
func (s *Store) ByRefText(ctx context.Context, q string) ([]Result, error) {
	rows, err := s.pool.Query(ctx, resultSelect+`
		WHERE EXISTS (
			SELECT 1 FROM commit_details cd
			WHERE cd.commit_id = c.id AND cd.kind = 'ref' AND cd.value ILIKE '%' || $1 || '%'
		)
		ORDER BY c.committed_at ASC`, q)
	if err != nil {
		return nil, fmt.Errorf("by ref text: %w", err)
	}
	return scanResults(rows)
}

// ListRepos returns the indexed repository names, alphabetically.
func (s *Store) ListRepos(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT name FROM repos ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan repo: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
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
