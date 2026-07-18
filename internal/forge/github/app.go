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
	"fmt"
	"net/http"
	"sync"
	"time"
)

// AppAuth authenticates as a GitHub App installation: a short-lived RS256
// app JWT is exchanged for an installation access token, which is cached
// and refreshed before expiry. Preferred over a PAT: per-repo installation
// scope and hourly-expiring tokens.
type AppAuth struct {
	base           string
	appID          string
	installationID string
	key            *rsa.PrivateKey
	http           *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// NewAppAuth parses the PEM private key (PKCS#1 or PKCS#8) and builds the
// authenticator. base "" means https://api.github.com.
func NewAppAuth(base, appID, installationID, privateKeyPEM string) (*AppAuth, error) {
	if base == "" {
		base = "https://api.github.com"
	}
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("github app: no PEM block in private key")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else if k8, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := k8.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("github app: private key is not RSA")
		}
		key = rsaKey
	} else {
		return nil, fmt.Errorf("github app: cannot parse private key: %w", err)
	}
	return &AppAuth{
		base:           base,
		appID:          appID,
		installationID: installationID,
		key:            key,
		http:           &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// Token returns a valid installation token, refreshing when within the
// early-refresh margin of expiry.
//
// @intent provide a valid GitHub App installation token to every API call, hiding the JWT-exchange and hourly rotation
// @domainRule the cached token is reused until within the early-refresh margin of expiry, then re-fetched
// @sideEffect on refresh, signs an app JWT and calls the GitHub access-tokens endpoint
// @mutates the cached token and expiry
func (a *AppAuth) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Until(a.expiry) > 2*time.Minute {
		return a.token, nil
	}

	appJWT, err := a.jwt(time.Now())
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/app/installations/%s/access_tokens", a.base, a.installationID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := a.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("github app token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("github app token: status %d", resp.StatusCode)
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("github app token: %w", err)
	}
	a.token, a.expiry = out.Token, out.ExpiresAt
	return a.token, nil
}

// jwt builds the RS256-signed app JWT (iss=appID, 60s clock-skew backdate,
// 9-minute lifetime — GitHub's maximum is 10). Hand-rolled: one fixed
// header and a single signature do not justify a JWT dependency.
func (a *AppAuth) jwt(now time.Time) (string, error) {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	header := enc(map[string]string{"alg": "RS256", "typ": "JWT"})
	payload := enc(map[string]any{
		"iss": a.appID,
		"iat": now.Unix() - 60,
		"exp": now.Add(9 * time.Minute).Unix(),
	})
	signing := header + "." + payload
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// NewClientWithTokenFunc builds a REST client whose bearer token is
// resolved per request (installation tokens rotate hourly).
func NewClientWithTokenFunc(base string, tokenFn func(context.Context) (string, error)) *Client {
	if base == "" {
		base = "https://api.github.com"
	}
	return &Client{base: base, tokenFn: tokenFn, http: &http.Client{Timeout: 10 * time.Second}}
}
