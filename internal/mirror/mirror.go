// Package mirror maintains bare repository mirrors for serve's merge-time
// ingestion (docs/serve-design.md M3). Tokens are used in-memory only; the
// on-disk mirror stores no credentials.
package mirror

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Sync clones (first time) or fetches (afterwards) a mirror of cloneURL
// under cacheDir, keyed by fullName. Returns the mirror path.
func Sync(cacheDir, fullName, cloneURL, token string) (string, error) {
	path := filepath.Join(cacheDir, sanitize(fullName)+".git")

	var auth *githttp.BasicAuth
	if token != "" {
		// GitHub accepts any username with a token password.
		auth = &githttp.BasicAuth{Username: "x-access-token", Password: token}
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return "", fmt.Errorf("create cache dir: %w", err)
		}
		_, err := git.PlainClone(path, true, &git.CloneOptions{
			URL:    cloneURL,
			Mirror: true,
			Auth:   auth,
		})
		if err != nil {
			_ = os.RemoveAll(path) // partial clone would break future syncs
			return "", fmt.Errorf("mirror clone %s: %w", fullName, err)
		}
		return path, nil
	} else if err != nil {
		return "", err
	}

	repo, err := git.PlainOpen(path)
	if err != nil {
		return "", fmt.Errorf("open mirror %s: %w", fullName, err)
	}
	err = repo.Fetch(&git.FetchOptions{Auth: auth, Force: true, Prune: true})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return "", fmt.Errorf("mirror fetch %s: %w", fullName, err)
	}
	return path, nil
}

func sanitize(fullName string) string {
	return strings.ReplaceAll(fullName, "/", "__")
}
