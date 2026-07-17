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
	"github.com/tae2089/context-diary/internal/ingest"
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
			"base": {"repo": {
				"full_name": "acme/shop",
				"clone_url": "https://example.invalid/acme/shop.git",
				"default_branch": "main"
			}}
		}
	}`, action, body, merged)
}

type recorded struct {
	comments []string
	ingests  []*github.PREvent
}

func testHandler(rec *recorded) http.HandlerFunc {
	return webhookHandler(serveDeps{
		secret: []byte(testSecret),
		comment: func(_ context.Context, fullName string, number int, body string) error {
			rec.comments = append(rec.comments, fmt.Sprintf("%s#%d:%s", fullName, number, body))
			return nil
		},
		ingestPR: func(_ context.Context, ev *github.PREvent) (ingest.Result, error) {
			rec.ingests = append(rec.ingests, ev)
			return ingest.Result{Inserted: 2, Scanned: 3}, nil
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
	if len(rec.comments)+len(rec.ingests) != 0 {
		t.Error("side effects despite invalid signature")
	}
}

func TestWebhookEditedUpsertsComment(t *testing.T) {
	rec := &recorded{}
	h := testHandler(rec)
	w := httptest.NewRecorder()
	h(w, signedRequest(t, "pull_request", prPayload("edited", false, "no trailers here")))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
	if len(rec.comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(rec.comments))
	}
	if !strings.Contains(rec.comments[0], "acme/shop#5") || !strings.Contains(rec.comments[0], "missing-why") {
		t.Errorf("comment = %q", rec.comments[0])
	}
	if len(rec.ingests) != 0 {
		t.Error("edited action must not ingest")
	}
}

func TestWebhookMergedIngests(t *testing.T) {
	rec := &recorded{}
	h := testHandler(rec)
	w := httptest.NewRecorder()
	h(w, signedRequest(t, "pull_request", prPayload("closed", true, "whatever")))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
	if len(rec.ingests) != 1 || rec.ingests[0].FullName != "acme/shop" {
		t.Fatalf("ingests = %+v", rec.ingests)
	}
	if len(rec.comments) != 0 {
		t.Error("merge must not comment")
	}
}

func TestWebhookClosedUnmergedIgnored(t *testing.T) {
	rec := &recorded{}
	h := testHandler(rec)
	w := httptest.NewRecorder()
	h(w, signedRequest(t, "pull_request", prPayload("closed", false, "x")))
	if w.Code != http.StatusOK || len(rec.ingests)+len(rec.comments) != 0 {
		t.Errorf("closed-unmerged: status=%d side effects=%d", w.Code, len(rec.ingests)+len(rec.comments))
	}
}

func TestWebhookNonPREventIgnored(t *testing.T) {
	rec := &recorded{}
	h := testHandler(rec)
	w := httptest.NewRecorder()
	h(w, signedRequest(t, "push", `{"ref": "refs/heads/main"}`))
	if w.Code != http.StatusOK || len(rec.ingests)+len(rec.comments) != 0 {
		t.Errorf("push event: status=%d side effects=%d", w.Code, len(rec.ingests)+len(rec.comments))
	}
}
