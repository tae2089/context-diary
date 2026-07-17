package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tae2089/context-diary/internal/forge/github"
)

const testSecret = "hook-secret"

func signedRequest(t *testing.T, event, payload string) *http.Request {
	t.Helper()
	m := hmac.New(sha256.New, []byte(testSecret))
	m.Write([]byte(payload))
	req := httptest.NewRequest("POST", "/webhook/github", strings.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(m.Sum(nil)))
	req.Header.Set("X-GitHub-Event", event)
	return req
}

func prPayload(action string, merged bool, body string) string {
	return fmt.Sprintf(`{
		"action": %q,
		"number": 5,
		"pull_request": {
			"body": %q,
			"merged": %v,
			"merge_commit_sha": "mmm999",
			"head": {"sha": "hhh111"},
			"base": {"repo": {
				"full_name": "acme/shop",
				"clone_url": "https://example.invalid/acme/shop.git",
				"default_branch": "main"
			}}
		}
	}`, action, body, merged)
}

type recorded struct {
	comments  []string
	statuses  []string // "sha:state:context"
	enqueued  []string
	queueFull bool
}

func testHandler(rec *recorded) http.HandlerFunc {
	return webhookHandler(serveDeps{
		secret: []byte(testSecret),
		comment: func(_ context.Context, fullName string, number int, body string) error {
			rec.comments = append(rec.comments, fmt.Sprintf("%s#%d:%s", fullName, number, body))
			return nil
		},
		status: func(_ context.Context, fullName, sha, state, statusContext, _ string) error {
			rec.statuses = append(rec.statuses, fmt.Sprintf("%s:%s:%s", sha, state, statusContext))
			return nil
		},
		enqueue: func(ev *github.PREvent) bool {
			if rec.queueFull {
				return false
			}
			rec.enqueued = append(rec.enqueued, ev.FullName)
			return true
		},
	})
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	rec := &recorded{}
	h := testHandler(rec)
	req := httptest.NewRequest("POST", "/webhook/github", strings.NewReader(`{}`))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	req.Header.Set("X-GitHub-Event", "pull_request")
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if len(rec.comments)+len(rec.enqueued)+len(rec.statuses) != 0 {
		t.Error("side effects despite invalid signature")
	}
}

func TestWebhookEditedCommentsAndSetsFailureStatus(t *testing.T) {
	rec := &recorded{}
	h := testHandler(rec)
	w := httptest.NewRecorder()
	h(w, signedRequest(t, "pull_request", prPayload("edited", false, "no trailers here")))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
	if len(rec.comments) != 1 || !strings.Contains(rec.comments[0], "missing-why") {
		t.Fatalf("comments = %v", rec.comments)
	}
	if len(rec.statuses) != 1 || rec.statuses[0] != "hhh111:failure:context-diary/context" {
		t.Errorf("statuses = %v", rec.statuses)
	}
	if len(rec.enqueued) != 0 {
		t.Error("edited action must not enqueue")
	}
}

func TestWebhookEditedCleanBodySetsSuccessStatus(t *testing.T) {
	rec := &recorded{}
	h := testHandler(rec)
	w := httptest.NewRecorder()
	body := "Fix the race.\n\nContext-Why: refund raced with settlement\n"
	h(w, signedRequest(t, "pull_request", prPayload("edited", false, body)))
	if len(rec.statuses) != 1 || rec.statuses[0] != "hhh111:success:context-diary/context" {
		t.Errorf("statuses = %v", rec.statuses)
	}
}

func TestWebhookMergedEnqueuesWithPendingStatus(t *testing.T) {
	rec := &recorded{}
	h := testHandler(rec)
	w := httptest.NewRecorder()
	h(w, signedRequest(t, "pull_request", prPayload("closed", true, "whatever")))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body)
	}
	if len(rec.enqueued) != 1 || rec.enqueued[0] != "acme/shop" {
		t.Fatalf("enqueued = %v", rec.enqueued)
	}
	if len(rec.statuses) != 1 || rec.statuses[0] != "mmm999:pending:context-diary/ingest" {
		t.Errorf("statuses = %v", rec.statuses)
	}
	if len(rec.comments) != 0 {
		t.Error("merge must not comment")
	}
}

func TestWebhookMergedQueueFull(t *testing.T) {
	rec := &recorded{queueFull: true}
	h := testHandler(rec)
	w := httptest.NewRecorder()
	h(w, signedRequest(t, "pull_request", prPayload("closed", true, "x")))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	if len(rec.statuses) != 0 {
		t.Error("no pending status when enqueue failed")
	}
}

func TestWebhookClosedUnmergedIgnored(t *testing.T) {
	rec := &recorded{}
	h := testHandler(rec)
	w := httptest.NewRecorder()
	h(w, signedRequest(t, "pull_request", prPayload("closed", false, "x")))
	if w.Code != http.StatusOK || len(rec.enqueued)+len(rec.comments)+len(rec.statuses) != 0 {
		t.Errorf("closed-unmerged: status=%d side effects", w.Code)
	}
}

func TestGithubTokenFnSelection(t *testing.T) {
	// PAT wins
	t.Setenv("GITHUB_TOKEN", "ghp_x")
	t.Setenv("GITHUB_APP_ID", "1")
	fn, kind, err := githubTokenFn()
	if err != nil || !strings.Contains(kind, "personal access token") {
		t.Fatalf("PAT selection: kind=%q err=%v", kind, err)
	}
	if tok, _ := fn(context.Background()); tok != "ghp_x" {
		t.Errorf("token = %q", tok)
	}

	// neither configured
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_APP_ID", "")
	if _, _, err := githubTokenFn(); err == nil {
		t.Error("expected error with no auth configured")
	}

	// app credentials incomplete
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_APP_INSTALLATION_ID", "2")
	if _, _, err := githubTokenFn(); err == nil {
		t.Error("expected error without private key")
	}
}

func TestBearerAuth(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "reached")
	})
	h := bearerAuth("sekrit", next)

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic sekrit", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"correct", "Bearer sekrit", http.StatusOK},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "/mcp", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != c.want {
			t.Errorf("%s: status = %d, want %d", c.name, w.Code, c.want)
		}
		if c.want == http.StatusOK && w.Body.String() != "reached" {
			t.Errorf("%s: next handler not reached", c.name)
		}
	}
}

func TestWebhookNonPREventIgnored(t *testing.T) {
	rec := &recorded{}
	h := testHandler(rec)
	w := httptest.NewRecorder()
	h(w, signedRequest(t, "push", `{"ref": "refs/heads/main"}`))
	if w.Code != http.StatusOK || len(rec.enqueued)+len(rec.comments)+len(rec.statuses) != 0 {
		t.Errorf("push event: status=%d side effects", w.Code)
	}
}
