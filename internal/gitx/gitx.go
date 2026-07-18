// Package gitx wraps the git CLI for the hook and lint paths. Hooks always
// run where git exists, so exec-ing git keeps the binary dependency-free;
// go-git is reserved for the future indexer.
package gitx

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// DiffPatchCap bounds the patch portion sent to an LLM (design R4). The
// --stat summary is always included in full.
const DiffPatchCap = 32 * 1024

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// StagedDiff returns the staged changes: full --stat plus a size-capped patch.
//
// @intent give the hook the staged changes as context, bounded so a huge diff cannot blow the budget
// @sideEffect runs git diff --cached
func StagedDiff(dir string) (string, error) {
	stat, err := run(dir, "diff", "--cached", "--stat")
	if err != nil {
		return "", err
	}
	patch, err := run(dir, "diff", "--cached")
	if err != nil {
		return "", err
	}
	if len(patch) > DiffPatchCap {
		patch = patch[:DiffPatchCap] + "\n[... patch truncated ...]\n"
	}
	if strings.TrimSpace(stat) == "" && strings.TrimSpace(patch) == "" {
		return "", nil
	}
	return stat + "\n" + patch, nil
}

// CommentChar resolves core.commentChar. Default "#"; the rare "auto" setting
// is resolved by git after our hook runs, so we fall back to "#" (design R3).
//
// @intent resolve the git comment character so injected template lines match the editor
// @domainRule the rare core.commentChar=auto is resolved by git after this hook runs, so it falls back to "#" (design R3)
func CommentChar(dir string) string {
	out, err := run(dir, "config", "core.commentChar")
	c := strings.TrimSpace(out)
	if err != nil || c == "" || c == "auto" {
		return "#"
	}
	return c
}

// Branch returns the current branch name, or "" when detached.
func Branch(dir string) string {
	out, err := run(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// HooksDir returns the directory git will read hooks from, resolving
// core.hooksPath when set (design N1). The second result reports whether a
// custom hooksPath (hook manager territory) is in effect.
//
// @intent locate where git reads hooks, distinguishing a custom core.hooksPath (hook-manager territory) from the default
// @domainRule a custom hooksPath is reported so init refuses to write there and prints manual instructions instead
func HooksDir(dir string) (path string, custom bool, err error) {
	if out, err := run(dir, "config", "core.hooksPath"); err == nil && strings.TrimSpace(out) != "" {
		p := strings.TrimSpace(out)
		if !filepath.IsAbs(p) {
			top, err := TopLevel(dir)
			if err != nil {
				return "", true, err
			}
			p = filepath.Join(top, p)
		}
		return p, true, nil
	}
	out, err := run(dir, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", false, err
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return p, false, nil
}

// TopLevel returns the repository root.
func TopLevel(dir string) (string, error) {
	out, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CommitMessage returns the full commit message body of a revision.
func CommitMessage(dir, rev string) (string, error) {
	return run(dir, "log", "-1", "--format=%B", rev)
}

// RevList returns non-merge commit hashes in the given range.
func RevList(dir, revRange string) ([]string, error) {
	out, err := run(dir, "rev-list", "--no-merges", revRange)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(out)
	return fields, nil
}
