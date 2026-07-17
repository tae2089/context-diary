// Package github is the GitHub forge adapter for serve
// (docs/serve-design.md): webhook verification, event parsing, and the
// three comment REST calls. Hand-rolled over net/http — a full client
// library is not justified for this surface.
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
	"time"
)

// ValidSignature checks the X-Hub-Signature-256 header (constant-time).
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

// PREvent is the subset of a pull_request webhook payload serve needs.
type PREvent struct {
	Action        string
	Number        int
	Body          string
	Merged        bool
	FullName      string // "owner/repo"
	CloneURL      string
	DefaultBranch string
}

// ParsePREvent extracts a PREvent from a verified payload.
func ParsePREvent(payload []byte) (*PREvent, error) {
	var raw struct {
		Action      string `json:"action"`
		Number      int    `json:"number"`
		PullRequest struct {
			Body   string `json:"body"`
			Merged bool   `json:"merged"`
			Base   struct {
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
		Action:        raw.Action,
		Number:        raw.Number,
		Body:          raw.PullRequest.Body,
		Merged:        raw.PullRequest.Merged,
		FullName:      raw.PullRequest.Base.Repo.FullName,
		CloneURL:      raw.PullRequest.Base.Repo.CloneURL,
		DefaultBranch: raw.PullRequest.Base.Repo.DefaultBranch,
	}, nil
}

// Client talks to the GitHub REST API.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// NewClient builds a client; base "" means https://api.github.com.
func NewClient(base, token string) *Client {
	if base == "" {
		base = "https://api.github.com"
	}
	return &Client{base: base, token: token, http: &http.Client{Timeout: 10 * time.Second}}
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
	req.Header.Set("Authorization", "Bearer "+c.token)
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

// UpsertComment maintains exactly one bot comment per PR, identified by
// marker: update in place when found, create otherwise (design W6-W7).
func (c *Client) UpsertComment(ctx context.Context, fullName string, number int, marker, body string) error {
	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	path := fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100", fullName, number)
	if err := c.do(ctx, "GET", path, nil, &comments); err != nil {
		return err
	}
	for _, cm := range comments {
		if strings.Contains(cm.Body, marker) {
			return c.do(ctx, "PATCH",
				fmt.Sprintf("/repos/%s/issues/comments/%d", fullName, cm.ID),
				map[string]string{"body": body}, nil)
		}
	}
	return c.do(ctx, "POST",
		fmt.Sprintf("/repos/%s/issues/%d/comments", fullName, number),
		map[string]string{"body": body}, nil)
}
