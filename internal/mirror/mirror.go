// Package mirror maintains bare repository mirrors for serve's merge-time
// ingestion (docs/serve-design.md M3). Tokens are used in-memory only; the
// on-disk mirror stores no credentials.
//
// @index Maintains bare git mirrors for merge-time ingestion; tokens are used in-memory only, never stored on disk.
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

// Path is where fullName's mirror lives under cacheDir.
func Path(cacheDir, fullName string) string {
	return filepath.Join(cacheDir, sanitize(fullName)+".git")
}

// Sync clones (first time) or fetches (afterwards) a mirror of cloneURL
// under cacheDir, keyed by fullName. Returns the mirror path.
//
// @intent keep a local bare mirror of a repository so merge-time ingestion has git history without a working tree
// @domainRule the token is used for in-memory auth only; the on-disk bare mirror never stores credentials
// @sideEffect clones or fetches into cacheDir over the network using the GitHub token
// @requires cacheDir is writable
// @ensures a partial clone is removed so a later Sync retries cleanly
func Sync(cacheDir, fullName, cloneURL, token string) (string, error) {
	path := Path(cacheDir, fullName)

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
