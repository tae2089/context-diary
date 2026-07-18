package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testKeyPEM(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return string(block), key
}

func TestAppJWT(t *testing.T) {
	pemStr, key := testKeyPEM(t)
	app, err := NewAppAuth("", "12345", "678", pemStr)
	if err != nil {
		t.Fatalf("NewAppAuth: %v", err)
	}
	jwt, err := app.jwt(time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatalf("jwt: %v", err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Iss != "12345" || claims.Iat != 1_800_000_000-60 || claims.Exp != 1_800_000_000+9*60 {
		t.Errorf("claims = %+v", claims)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("signature does not verify: %v", err)
	}
}

func TestInstallationTokenCachedAndRefreshed(t *testing.T) {
	pemStr, _ := testKeyPEM(t)
	var calls atomic.Int32
	expiry := time.Now().Add(2 * time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/app/installations/678/access_tokens" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			t.Errorf("missing app JWT bearer: %q", r.Header.Get("Authorization"))
		}
		n := calls.Add(1)
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_tok" + string(rune('0'+n)),
			"expires_at": expiry.Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	app, err := NewAppAuth(srv.URL, "12345", "678", pemStr)
	if err != nil {
		t.Fatal(err)
	}
	tok1, err := app.Token(t.Context())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	tok2, err := app.Token(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if tok1 != tok2 || calls.Load() != 1 {
		t.Errorf("token not cached: %q vs %q, %d calls", tok1, tok2, calls.Load())
	}

	app.expiry = time.Now().Add(-time.Minute) // force refresh
	tok3, err := app.Token(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if tok3 == tok1 || calls.Load() != 2 {
		t.Errorf("token not refreshed after expiry: %q, %d calls", tok3, calls.Load())
	}
}

func TestClientUsesTokenFunc(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := NewClientWithTokenFunc(srv.URL, func(context.Context) (string, error) { return "dyn-token", nil })
	if err := c.SetStatus(t.Context(), "acme/shop", "abc", StatusSuccess, "ctx", "d", ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if gotAuth != "Bearer dyn-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}
