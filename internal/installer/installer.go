// Package installer implements `context-diary init` (design N1-N9): hook
// scripts and config scaffolding. It never modifies files it does not own —
// a foreign hook yields a manual instruction instead (design R5).
//
// @index context-diary init: coexistence-safe git hook installation, config scaffold, and AI-agent convention snippets.
package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Marker identifies hook scripts owned by context-diary.
const Marker = "managed by 'context-diary init'"

// Hooks lists the git hooks context-diary installs.
var Hooks = []string{"prepare-commit-msg", "commit-msg"}

// Install statuses.
const (
	StatusInstalled = "installed"
	StatusUpdated   = "updated"
	StatusManual    = "manual"
)

// Result reports what happened for one hook slot.
type Result struct {
	Hook        string
	Status      string
	Instruction string // set when Status == StatusManual
}

const configScaffold = `# context-diary configuration — committed; never put secrets here.
# Docs: https://github.com/tae2089/context-diary/blob/main/docs/cli-design.md

# Shared scope list (top-level keys must precede tables).
# scopes = ["order/cancel", "payment/refund"]

[hook]
# comment: trailer template inserted as comments in the commit editor (default)
# off:     disable the prepare-commit-msg hook
mode = "comment"

[lint]
# warn: commit-msg hook only warns; strict: violations block the commit.
# strict is recommended when commits are authored by AI coding agents:
# the violation output is the feedback they use to fix and retry.
level = "warn"
`

func script(hookName string) string {
	return fmt.Sprintf(`#!/bin/sh
# %s — do not edit; re-run 'context-diary init' to update
command -v context-diary >/dev/null 2>&1 || exit 0
exec context-diary hook %s "$@"
`, Marker, hookName)
}

// Install writes hook scripts into hooksDir. Empty slot or our own script →
// write; anything else → untouched plus a manual instruction.
//
// @intent install the git hooks without clobbering another tool's hooks
// @domainRule never edit files it does not own (I-3): a foreign hook is left untouched and returned as a manual instruction
// @sideEffect writes hook scripts into the git hooks directory
func Install(hooksDir string) ([]Result, error) {
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return nil, fmt.Errorf("create hooks dir: %w", err)
	}
	var results []Result
	for _, h := range Hooks {
		path := filepath.Join(hooksDir, h)
		existing, err := os.ReadFile(path)
		switch {
		case os.IsNotExist(err):
			if err := writeScript(path, h); err != nil {
				return nil, err
			}
			results = append(results, Result{Hook: h, Status: StatusInstalled})
		case err != nil:
			return nil, fmt.Errorf("read %s: %w", path, err)
		case strings.Contains(string(existing), Marker):
			if err := writeScript(path, h); err != nil {
				return nil, err
			}
			results = append(results, Result{Hook: h, Status: StatusUpdated})
		default:
			results = append(results, Result{
				Hook:   h,
				Status: StatusManual,
				Instruction: fmt.Sprintf(
					"existing %s hook found; add this line to it manually:\n  context-diary hook %s \"$@\"", h, h),
			})
		}
	}
	return results, nil
}

func writeScript(path, hookName string) error {
	if err := os.WriteFile(path, []byte(script(hookName)), 0o755); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// ScaffoldConfig writes .context-diary.toml into dir when absent (design N8).
//
// @intent write a starter .context-diary.toml when none exists, without overwriting an edited one
// @sideEffect creates the config file when absent
func ScaffoldConfig(dir string) (created bool, err error) {
	path := filepath.Join(dir, ".context-diary.toml")
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.WriteFile(path, []byte(configScaffold), 0o644); err != nil {
		return false, fmt.Errorf("write config scaffold: %w", err)
	}
	return true, nil
}
