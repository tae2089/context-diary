package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestValidSignature(t *testing.T) {
	secret := []byte("s3cret")
	body := []byte(`{"x":1}`)
	if !ValidSignature(secret, body, sign(secret, body)) {
		t.Error("valid signature rejected")
	}
	if ValidSignature(secret, body, sign([]byte("wrong"), body)) {
		t.Error("wrong-secret signature accepted")
	}
	if ValidSignature(secret, body, "") {
		t.Error("missing signature accepted")
	}
	if ValidSignature(secret, body, "sha256=zzzz") {
		t.Error("garbage signature accepted")
	}
}

func TestParsePREvent(t *testing.T) {
	payload := []byte(`{
		"action": "edited",
		"number": 7,
		"pull_request": {
			"body": "PR body text",
			"merged": false,
			"merge_commit_sha": "mmm999",
			"head": {"sha": "hhh111"},
			"base": {"repo": {
				"full_name": "acme/shop",
				"clone_url": "https://github.com/acme/shop.git",
				"default_branch": "main"
			}}
		}
	}`)
	ev, err := ParsePREvent(payload)
	if err != nil {
		t.Fatalf("ParsePREvent: %v", err)
	}
	if ev.Action != "edited" || ev.Number != 7 || ev.FullName != "acme/shop" ||
		ev.Body != "PR body text" || ev.Merged || ev.DefaultBranch != "main" ||
		ev.CloneURL != "https://github.com/acme/shop.git" ||
		ev.HeadSHA != "hhh111" || ev.MergeCommitSHA != "mmm999" {
		t.Errorf("parsed event = %+v", ev)
	}
}

func TestSetStatus(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/repos/acme/shop/statuses/abc123" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(201)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "tok")
	if err := c.SetStatus(t.Context(), "acme/shop", "abc123", StatusSuccess, "context-diary/ingest", "indexed 3 entries"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if got["state"] != "success" || got["context"] != "context-diary/ingest" || got["description"] != "indexed 3 entries" {
		t.Errorf("status body = %v", got)
	}
}

// stubAPI returns a fake GitHub REST server recording comment operations.
func stubAPI(t *testing.T, existing []map[string]any) (*httptest.Server, *[]string) {
	t.Helper()
	var ops []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/acme/shop/issues/7/comments":
			json.NewEncoder(w).Encode(existing)
		case r.Method == "POST" && r.URL.Path == "/repos/acme/shop/issues/7/comments":
			ops = append(ops, "create")
			w.WriteHeader(201)
			w.Write([]byte(`{"id": 1}`))
		case r.Method == "PATCH" && r.URL.Path == "/repos/acme/shop/issues/comments/42":
			ops = append(ops, "update")
			w.Write([]byte(`{"id": 42}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &ops
}

func TestUpsertCommentCreatesWhenAbsent(t *testing.T) {
	srv, ops := stubAPI(t, []map[string]any{
		{"id": 9, "body": "unrelated human comment"},
	})
	c := NewClient(srv.URL, "test-token")
	if err := c.UpsertComment(t.Context(), "acme/shop", 7, "<!-- context-diary -->", "hello"); err != nil {
		t.Fatalf("UpsertComment: %v", err)
	}
	if len(*ops) != 1 || (*ops)[0] != "create" {
		t.Errorf("ops = %v, want [create]", *ops)
	}
}

func TestUpsertCommentUpdatesWhenMarkerFound(t *testing.T) {
	srv, ops := stubAPI(t, []map[string]any{
		{"id": 9, "body": "unrelated"},
		{"id": 42, "body": "<!-- context-diary -->\nold content"},
	})
	c := NewClient(srv.URL, "test-token")
	if err := c.UpsertComment(t.Context(), "acme/shop", 7, "<!-- context-diary -->", "new content"); err != nil {
		t.Fatalf("UpsertComment: %v", err)
	}
	if len(*ops) != 1 || (*ops)[0] != "update" {
		t.Errorf("ops = %v, want [update]", *ops)
	}
}
