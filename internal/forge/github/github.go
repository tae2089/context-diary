// Package github is the GitHub forge adapter for serve
// (docs/serve-design.md): webhook verification, event parsing, and the
// three comment REST calls. Hand-rolled over net/http — a full client
// library is not justified for this surface.
//
// @index GitHub forge adapter for serve: webhook HMAC verification, PR event parsing, commit statuses, comments, App auth.
package github

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Status states accepted by the GitHub statuses API.
const (
	StatusPending = "pending"
	StatusSuccess = "success"
	StatusFailure = "failure"
	StatusError   = "error"
)

// PREvent is the subset of a pull_request webhook payload serve needs.
type PREvent struct {
	Action         string
	Number         int
	Body           string
	Merged         bool
	HeadSHA        string // PR head — lint status target
	MergeCommitSHA string // set once merged — ingest status target
	FullName       string // "owner/repo"
	CloneURL       string
	DefaultBranch  string
}

// Client talks to the GitHub REST API. The bearer token is resolved per
// request: static for a PAT, rotating for a GitHub App installation.
type Client struct {
	base    string
	tokenFn func(context.Context) (string, error)
	http    *http.Client
}

// PRCommit is one commit of a pull request branch.
type PRCommit struct {
	SHA     string
	Message string
	Merge   bool // more than one parent
}

// ValidSignature checks the X-Hub-Signature-256 header (constant-time).
//
// @intent authenticate that a webhook payload really came from GitHub before any side effect
// @domainRule webhook bodies are untrusted until this passes; a bad or missing signature must be rejected with 401
// @requires secret is the shared webhook secret configured on the GitHub App
// @ensures comparison is constant-time to avoid timing oracles
func ValidSignature(secret, body []byte, sigHeader string) bool {
	want, ok := strings.CutPrefix(sigHeader, "sha256=")
	if !ok {
		return false
	}
	wantRaw, err := hex.DecodeString(want)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hmac.Equal(m.Sum(nil), wantRaw)
}

// ParsePREvent extracts a PREvent from a verified payload.
//
// @intent extract the pull_request fields serve needs from a verified webhook payload
// @requires the payload signature was already verified (parse only after ValidSignature)
func ParsePREvent(payload []byte) (*PREvent, error) {
	var raw struct {
		Action      string `json:"action"`
		Number      int    `json:"number"`
		PullRequest struct {
			Body           string `json:"body"`
			Merged         bool   `json:"merged"`
			MergeCommitSHA string `json:"merge_commit_sha"`
			Head           struct {
				SHA string `json:"sha"`
			} `json:"head"`
			Base struct {
				Repo struct {
					FullName      string `json:"full_name"`
					CloneURL      string `json:"clone_url"`
					DefaultBranch string `json:"default_branch"`
				} `json:"repo"`
			} `json:"base"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("parse pull_request payload: %w", err)
	}
	return &PREvent{
		Action:         raw.Action,
		Number:         raw.Number,
		Body:           raw.PullRequest.Body,
		Merged:         raw.PullRequest.Merged,
		HeadSHA:        raw.PullRequest.Head.SHA,
		MergeCommitSHA: raw.PullRequest.MergeCommitSHA,
		FullName:       raw.PullRequest.Base.Repo.FullName,
		CloneURL:       raw.PullRequest.Base.Repo.CloneURL,
		DefaultBranch:  raw.PullRequest.Base.Repo.DefaultBranch,
	}, nil
}

// NewClient builds a client with a static token (PAT); base "" means
// https://api.github.com.
func NewClient(base, token string) *Client {
	return NewClientWithTokenFunc(base, func(context.Context) (string, error) { return token, nil })
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	token, err := c.tokenFn(ctx)
	if err != nil {
		return fmt.Errorf("resolve github token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github %s %s: status %d: %s", method, path, resp.StatusCode, b)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// SetStatus posts a commit status (docs/serve-design.md §Statuses).
// description is truncated to GitHub's 140-char limit; a non-empty
// targetURL becomes the status "Details" link.
//
// @intent surface context-diary results as a commit status that branch protection can require
// @sideEffect posts a commit status to the GitHub REST API
// @requires description fits GitHub's 140-char limit (truncated here otherwise)
func (c *Client) SetStatus(ctx context.Context, fullName, sha, state, statusContext, description, targetURL string) error {
	if len(description) > 140 {
		description = description[:137] + "..."
	}
	body := map[string]string{
		"state":       state,
		"context":     statusContext,
		"description": description,
	}
	if targetURL != "" {
		body["target_url"] = targetURL
	}
	return c.do(ctx, "POST", fmt.Sprintf("/repos/%s/statuses/%s", fullName, sha), body, nil)
}

// ListPRCommits returns the PR's branch commits (first page, 100 max —
// enough for the validation use; huge PRs are backstopped by lint on main).
//
// @intent fetch a PR's branch commits so the bot can validate the commit-path context carrier for merge/rebase teams
// @domainRule reads only the first 100 commits; larger PRs are an anti-pattern and are backstopped by lint on main
// @sideEffect calls the GitHub REST API
func (c *Client) ListPRCommits(ctx context.Context, fullName string, number int) ([]PRCommit, error) {
	var raw []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
	}
	path := fmt.Sprintf("/repos/%s/pulls/%d/commits?per_page=100", fullName, number)
	if err := c.do(ctx, "GET", path, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]PRCommit, 0, len(raw))
	for _, r := range raw {
		out = append(out, PRCommit{SHA: r.SHA, Message: r.Commit.Message, Merge: len(r.Parents) > 1})
	}
	return out, nil
}

// UpsertComment maintains exactly one bot comment per PR, identified by
// marker: update in place when found, create otherwise (design W6-W7).
// Returns the comment's html_url — the status "Details" target.
//
// @intent keep exactly one bot comment per PR so pushes never spam the thread
// @domainRule the comment is found by an HTML marker and updated in place; otherwise a new one is created
// @sideEffect creates or edits an issue comment via the GitHub REST API
// @return the comment's html_url, used as the commit-status Details target
func (c *Client) UpsertComment(ctx context.Context, fullName string, number int, marker, body string) (string, error) {
	var comments []struct {
		ID      int64  `json:"id"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
	}
	path := fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100", fullName, number)
	if err := c.do(ctx, "GET", path, nil, &comments); err != nil {
		return "", err
	}
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	for _, cm := range comments {
		if strings.Contains(cm.Body, marker) {
			err := c.do(ctx, "PATCH",
				fmt.Sprintf("/repos/%s/issues/comments/%d", fullName, cm.ID),
				map[string]string{"body": body}, &out)
			return out.HTMLURL, err
		}
	}
	err := c.do(ctx, "POST",
		fmt.Sprintf("/repos/%s/issues/%d/comments", fullName, number),
		map[string]string{"body": body}, &out)
	return out.HTMLURL, err
}
