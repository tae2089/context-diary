// Package funclog finds the commits that touched one function, via
// `git log -L` (docs: L3 in indexer-design.md — composition, not storage).
// It execs the git CLI: go-git has no line-level history, and every host
// that runs this (dev clones, serve mirror hosts) has git anyway. Works on
// bare mirrors — log needs no worktree.
package funclog

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Commit is one point in a function's history.
type Commit struct {
	Hash    string
	Subject string
}

var hashLine = regexp.MustCompile(`^([0-9a-f]{40})\t(.*)$`)

// CommitsTouching returns the commits on branch that changed the named
// function in file, oldest first. Function matching uses git's language-
// aware funcname patterns (`-L :<funcname>:<file>`).
func CommitsTouching(repoPath, branch, file, function string) ([]Commit, error) {
	rev := "HEAD"
	if branch != "" {
		rev = branch
	}
	// -L implies patch output; the %H<TAB>%s format line is filtered back
	// out below. --no-textconv keeps binary-adjacent configs from breaking.
	cmd := exec.Command("git", "log",
		"--format=%H%x09%s",
		"-L", fmt.Sprintf(":%s:%s", function, file),
		rev)
	cmd.Dir = repoPath
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git log -L %s:%s: %w: %s",
			function, file, err, strings.TrimSpace(errb.String()))
	}

	var newestFirst []Commit
	for _, line := range strings.Split(out.String(), "\n") {
		if m := hashLine.FindStringSubmatch(line); m != nil {
			newestFirst = append(newestFirst, Commit{Hash: m[1], Subject: m[2]})
		}
	}
	oldest := make([]Commit, len(newestFirst))
	for i, c := range newestFirst {
		oldest[len(newestFirst)-1-i] = c
	}
	return oldest, nil
}
