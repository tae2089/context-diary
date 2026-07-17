// Package mcptool exposes the context index over MCP
// (docs/serve-design.md §MCP endpoint) using the official
// modelcontextprotocol/go-sdk. Read-only; audience translation of the
// answers is the calling assistant's job.
package mcptool

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tae2089/context-diary/internal/store"
)

// Searcher is the store surface the tools need (consumer-owned interface).
type Searcher interface {
	Search(ctx context.Context, repoName string, q store.Query) ([]store.Result, error)
	ListScopes(ctx context.Context, repoName string) ([]store.ScopeCount, error)
}

type searchArgs struct {
	Repo  string `json:"repo,omitempty" jsonschema:"repository name; omit to search every indexed repository"`
	Scope string `json:"scope,omitempty" jsonschema:"scope slug to filter by, e.g. order/cancel"`
	Query string `json:"query,omitempty" jsonschema:"free-text search over subject, why, and body (websearch syntax)"`
	Since string `json:"since,omitempty" jsonschema:"RFC3339 lower bound for commit time"`
	Until string `json:"until,omitempty" jsonschema:"RFC3339 upper bound for commit time"`
	Limit int    `json:"limit,omitempty" jsonschema:"max entries to return (default 50)"`
}

type searchEntry struct {
	Repo        string   `json:"repo"`
	Hash        string   `json:"hash"`
	Subject     string   `json:"subject"`
	Why         string   `json:"why"`
	Scopes      []string `json:"scopes,omitempty"`
	Decisions   []string `json:"decisions,omitempty"`
	Refs        []string `json:"refs,omitempty"`
	Author      string   `json:"author,omitempty"`
	CommittedAt string   `json:"committed_at"`
}

type searchResult struct {
	Entries []searchEntry `json:"entries"`
}

type scopesArgs struct {
	Repo string `json:"repo,omitempty" jsonschema:"repository name; omit for every indexed repository"`
}

type scopesResult struct {
	Scopes []store.ScopeCount `json:"scopes"`
}

// NewServer builds the MCP server with the query tools registered.
func NewServer(s Searcher, version string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "context-diary",
		Title:   "context-diary — the why behind code changes",
		Version: version,
	}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "search_context",
		Description: "Search the context index: why code changed, which product scopes were touched, " +
			"and what tradeoffs were decided. Filter by repo, scope slug, free text, and time range. " +
			"Returns commit-level context entries, newest first.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args searchArgs) (*mcp.CallToolResult, searchResult, error) {
		q := store.Query{Scope: args.Scope, Text: args.Query, Limit: args.Limit}
		var err error
		if q.Since, err = parseTime(args.Since); err != nil {
			return nil, searchResult{}, fmt.Errorf("since: %w", err)
		}
		if q.Until, err = parseTime(args.Until); err != nil {
			return nil, searchResult{}, fmt.Errorf("until: %w", err)
		}
		rs, err := s.Search(ctx, args.Repo, q)
		if err != nil {
			return nil, searchResult{}, err
		}
		out := searchResult{Entries: make([]searchEntry, 0, len(rs))}
		for _, r := range rs {
			out.Entries = append(out.Entries, searchEntry{
				Repo:        r.Repo,
				Hash:        r.Hash,
				Subject:     r.Subject,
				Why:         r.Why,
				Scopes:      r.Scopes,
				Decisions:   r.Decisions,
				Refs:        r.Refs,
				Author:      r.AuthorName,
				CommittedAt: r.CommittedAt.Format(time.RFC3339),
			})
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_scopes",
		Description: "List the product scope slugs present in the context index with entry counts. " +
			"Use this to discover what areas exist before searching.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args scopesArgs) (*mcp.CallToolResult, scopesResult, error) {
		scopes, err := s.ListScopes(ctx, args.Repo)
		if err != nil {
			return nil, scopesResult{}, err
		}
		return nil, scopesResult{Scopes: scopes}, nil
	})

	return srv
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}
