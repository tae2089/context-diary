// Package hook orchestrates the prepare-commit-msg and commit-msg git hooks
// per docs/cli-design.md v0.2 (P1-P16, L1-L8). The never-block invariant
// (I-1) lives here: Prepare reports problems on Stderr and returns; it never
// fails the commit. Only CommitMsg in strict lint mode can block.
//
// @index prepare-commit-msg and commit-msg git hooks: template injection and trailer lint under the never-block invariant.
package hook

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tae2089/context-diary/internal/config"
	"github.com/tae2089/context-diary/internal/trailer"
)

// Deps are the injected dependencies of Prepare.
type Deps struct {
	Config      config.Config
	StagedDiff  func() (string, error)
	CommentChar string
	Stderr      io.Writer
}

// Result is the outcome of the commit-msg hook.
type Result struct {
	Violations []trailer.Violation
	Blocked    bool
}

func (d Deps) warnf(format string, args ...any) {
	if d.Stderr != nil {
		fmt.Fprintf(d.Stderr, "context-diary: "+format+"\n", args...)
	}
}

// Prepare implements the prepare-commit-msg flow (P1-P16): inject a commented
// trailer template for the developer to fill in. It edits msgFile in place
// when the template applies, and silently returns on every skip or failure
// path.
//
// @intent give a human committer a commented trailer template to fill in, without ever blocking the commit
// @domainRule never-block (I-1): every failure path warns to stderr and returns; the commit is never rejected here
// @domainRule skipped when mode is off, on merge/squash/-m sources, when Context-Why already present, or when the staged diff is empty
// @sideEffect overwrites the commit message file with the template inserted before git's comment section
// @mutates the commit message file at msgFile
func Prepare(deps Deps, msgFile, source string) {
	if deps.Config.Hook.Mode == config.ModeOff {
		return
	}
	// merge/squash carry machine-generated messages; -m ("message") never
	// opens the editor, so a commented template would be stripped unseen.
	if source == "merge" || source == "squash" || source == "message" {
		return
	}

	raw, err := os.ReadFile(msgFile)
	if err != nil {
		deps.warnf("cannot read commit message file: %v", err)
		return
	}
	msg := string(raw)

	if trailer.HasContextWhy(trailer.StripComments(msg, deps.CommentChar)) {
		return
	}

	diff, err := deps.StagedDiff()
	if err != nil {
		deps.warnf("cannot read staged diff: %v", err)
		return
	}
	if strings.TrimSpace(diff) == "" {
		return
	}

	block := trailer.CommentLines(trailer.Template(), deps.CommentChar)
	updated := insertBlock(msg, block, deps.CommentChar)
	if err := os.WriteFile(msgFile, []byte(updated), 0o644); err != nil {
		deps.warnf("cannot update commit message file: %v", err)
	}
}

// insertBlock places the template lines before git's trailing comment
// section, or at the end of the message when there is none.
func insertBlock(msg string, block []string, commentChar string) string {
	lines := strings.Split(strings.TrimRight(msg, "\n"), "\n")
	insertAt := len(lines)
	for i, l := range lines {
		if strings.HasPrefix(l, commentChar) {
			insertAt = i
			break
		}
	}
	var out []string
	out = append(out, lines[:insertAt]...)
	if insertAt > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
		out = append(out, "")
	}
	out = append(out, block...)
	if insertAt < len(lines) {
		out = append(out, "")
		out = append(out, lines[insertAt:]...)
	}
	return strings.Join(out, "\n") + "\n"
}

// CommitMsg implements the commit-msg flow (L1-L8): lint the final message,
// blocking only in strict mode. Violation messages are the feedback an AI
// agent uses to fix the message and retry.
//
// @intent lint the final commit message and, in strict mode, reject it so the AI agent self-corrects from the violation output
// @domainRule blocks the commit only when lint.level is strict and violations exist; warn mode never blocks
// @ensures Blocked is true only for strict mode with at least one violation
func CommitMsg(cfg config.Config, msg, commentChar string) Result {
	vs := trailer.Lint(trailer.StripComments(msg, commentChar))
	return Result{
		Violations: vs,
		Blocked:    cfg.Lint.Level == config.LevelStrict && len(vs) > 0,
	}
}
