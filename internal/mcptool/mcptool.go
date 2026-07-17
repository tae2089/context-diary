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

	"github.com/tae2089/context-diary/internal/funclog"
	"github.com/tae2089/context-diary/internal/store"
)

// Searcher is the store surface the tools need (consumer-owned interface).
type Searcher interface {
	Search(ctx context.Context, repoName string, q store.Query) ([]store.Result, error)
	ListScopes(ctx context.Context, repoName string) ([]store.ScopeCount, error)
	ByHashes(ctx context.Context, repoName string, hashes []string) ([]store.Result, error)
	ByRefText(ctx context.Context, q string) ([]store.Result, error)
	ReferencedBy(ctx context.Context, refRepo, refPath, symbol string) ([]store.Result, error)
}

// Deps wires the tools to their environment.
type Deps struct {
	Store Searcher
	// RepoPath resolves a repo name to a local clone/mirror path for
	// git-level queries (explain_function). Nil disables that tool's
	// registration.
	RepoPath func(repo string) (string, error)
	Version  string
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

type refArgs struct {
	Ref string `json:"ref" jsonschema:"text contained in Context-Ref values, e.g. a Jira key (PAY-77), a doc URL, or a code ref"`
}

// toSearchResult converts store rows to the wire shape.
func toSearchResult(rs []store.Result) searchResult {
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
	return out
}

type scopesResult struct {
	Scopes []store.ScopeCount `json:"scopes"`
}

// NewServer builds the MCP server with the query tools registered.
func NewServer(deps Deps) *mcp.Server {
	s := deps.Store
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "context-diary",
		Title:   "context-diary — the why behind code changes",
		Version: deps.Version,
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
		return nil, toSearchResult(rs), nil
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

	mcp.AddTool(srv, &mcp.Tool{
		Name: "related_by_ref",
		Description: "Entries across ALL repositories whose Context-Ref values contain the query " +
			"text — the cross-repo join for shared Jira tickets, design docs, ADRs, and " +
			"postmortem links. Use to answer 'which repos were touched by this ticket/incident'.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args refArgs) (*mcp.CallToolResult, searchResult, error) {
		if args.Ref == "" {
			return nil, searchResult{}, fmt.Errorf("ref is required")
		}
		rs, err := s.ByRefText(ctx, args.Ref)
		if err != nil {
			return nil, searchResult{}, err
		}
		return nil, toSearchResult(rs), nil
	})

	if deps.RepoPath != nil {
		mcp.AddTool(srv, &mcp.Tool{
			Name: "explain_function",
			Description: "Timeline of WHY one function changed: composes git line-level history " +
				"(git log -L) with the context index. Each timeline point carries the commit's " +
				"why/scopes/decisions when indexed; has_context=false marks coverage gaps " +
				"(candidates for backfill).",
		}, func(ctx context.Context, req *mcp.CallToolRequest, args explainArgs) (*mcp.CallToolResult, explainResult, error) {
			if args.Repo == "" || args.File == "" || args.Function == "" {
				return nil, explainResult{}, fmt.Errorf("repo, file, and function are required")
			}
			path, err := deps.RepoPath(args.Repo)
			if err != nil {
				return nil, explainResult{}, err
			}
			commits, err := funclog.CommitsTouching(path, args.Branch, args.File, args.Function)
			if err != nil {
				return nil, explainResult{}, err
			}
			hashes := make([]string, len(commits))
			for i, c := range commits {
				hashes[i] = c.Hash
			}
			indexed, err := s.ByHashes(ctx, args.Repo, hashes)
			if err != nil {
				return nil, explainResult{}, err
			}
			byHash := map[string]store.Result{}
			for _, r := range indexed {
				byHash[r.Hash] = r
			}
			out := explainResult{Function: args.Function, File: args.File}
			for _, c := range commits {
				p := explainPoint{Hash: c.Hash, Subject: c.Subject}
				if r, ok := byHash[c.Hash]; ok {
					p.HasContext = true
					p.Why = r.Why
					p.Scopes = r.Scopes
					p.Decisions = r.Decisions
					p.CommittedAt = r.CommittedAt.Format(time.RFC3339)
				}
				out.Timeline = append(out.Timeline, p)
			}
			// Cross-repo: entries anywhere whose code refs point at this function.
			refs, err := s.ReferencedBy(ctx, args.Repo, args.File, args.Function)
			if err != nil {
				return nil, explainResult{}, err
			}
			out.ReferencedBy = toSearchResult(refs).Entries
			return nil, out, nil
		})
	}

	return srv
}

type explainArgs struct {
	Repo     string `json:"repo" jsonschema:"repository name as indexed, e.g. owner/repo"`
	File     string `json:"file" jsonschema:"path of the file within the repository"`
	Function string `json:"function" jsonschema:"function or method name (git funcname matching)"`
	Branch   string `json:"branch,omitempty" jsonschema:"branch to trace; default HEAD"`
}

type explainPoint struct {
	Hash        string   `json:"hash"`
	Subject     string   `json:"subject"`
	HasContext  bool     `json:"has_context"`
	Why         string   `json:"why,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
	Decisions   []string `json:"decisions,omitempty"`
	CommittedAt string   `json:"committed_at,omitempty"`
}

type explainResult struct {
	File     string         `json:"file"`
	Function string         `json:"function"`
	Timeline []explainPoint `json:"timeline"`
	// ReferencedBy: entries in other repositories whose Context-Ref code
	// refs point at this function ("this decision concerns you").
	ReferencedBy []searchEntry `json:"referenced_by,omitempty"`
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}
