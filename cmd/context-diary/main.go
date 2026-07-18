// Command context-diary is the trailer-authoring CLI: git hook entry points,
// range linting, hook installation, and AI-agent convention setup. See
// docs/cli-design.md for the design and its invariants.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tae2089/context-diary/internal/config"
	"github.com/tae2089/context-diary/internal/gitx"
	"github.com/tae2089/context-diary/internal/hook"
	"github.com/tae2089/context-diary/internal/installer"
	"github.com/tae2089/context-diary/internal/trailer"
)

const usage = `context-diary — capture the "why" behind code changes in git trailers

Usage:
  context-diary init [--agent <claude-code|codex>]           install git hooks (+ agent convention file)
  context-diary instructions [claude-code|codex]             print the agent convention snippet
  context-diary hook prepare-commit-msg <file> [src [sha]]   (invoked by git)
  context-diary hook commit-msg <file>                       (invoked by git)
  context-diary lint <rev-range>                             validate trailers over commits (CI)
  context-diary lint-message [file|-]                        lint a PR description / message body (default: stdin)
  context-diary scopes                                       list configured scopes
  context-diary index [--repo <name>] [--rescan]              index context trailers into Postgres
  context-diary backfill [--branch <name>]                    list commits lacking context (for note backfill)
  context-diary explain <file> <function>                      why-timeline of one function (git log -L × index)
  context-diary serve [--addr :8080]                          GitHub PR bot + MCP endpoint

Environment:
  CONTEXT_DIARY_DB (or DATABASE_URL)   Postgres DSN ('index', 'serve')
  GITHUB_TOKEN                         PR comments + mirror clone ('serve')
  GITHUB_WEBHOOK_SECRET                webhook HMAC verification ('serve')
  CONTEXT_DIARY_ADMIN_TOKEN            bearer for POST /admin/rescan ('serve'; unset disables it)
`

func main() {
	os.Exit(run(os.Args[1:]))
}

// run dispatches the subcommand and returns the process exit code.
//
// @intent single entry point that routes the CLI subcommand (init/hook/lint/index/serve/backfill/explain/scopes/instructions)
func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "init":
		agent := ""
		if len(args) >= 3 && args[1] == "--agent" {
			agent = args[2]
		}
		return cmdInit(agent)
	case "instructions":
		fmt.Print(installer.Instructions())
		return 0
	case "hook":
		return cmdHook(args[1:])
	case "lint":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: context-diary lint <rev-range>")
			return 2
		}
		return cmdLint(args[1])
	case "lint-message":
		return cmdLintMessage(args[1:])
	case "scopes":
		return cmdScopes()
	case "index":
		return cmdIndex(args[1:])
	case "serve":
		return cmdServe(args[1:])
	case "backfill":
		return cmdBackfill(args[1:])
	case "explain":
		return cmdExplain(args[1:])
	default:
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
}

// loadConfig resolves config from the repo root and the user config dir.
func loadConfig() (config.Config, error) {
	repoFile := ".context-diary.toml"
	if top, err := gitx.TopLevel("."); err == nil {
		repoFile = filepath.Join(top, ".context-diary.toml")
	}
	userFile := ""
	if dir, err := os.UserConfigDir(); err == nil {
		userFile = filepath.Join(dir, "context-diary", "config.toml")
	}
	return config.Load(repoFile, userFile, os.Getenv)
}

func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "context-diary: "+format+"\n", args...)
}

// cmdInit installs the git hooks and scaffolds config, optionally setting up
// an AI-agent convention file.
//
// @intent implement `context-diary init`: install hooks, scaffold config, and optionally write the agent convention snippet
// @sideEffect writes hook scripts, .context-diary.toml, and (with --agent) a CLAUDE.md/AGENTS.md snippet
func cmdInit(agent string) int {
	hooksDir, custom, err := gitx.HooksDir(".")
	if err != nil {
		warnf("not a git repository? %v", err)
		return 1
	}
	if custom {
		fmt.Printf("core.hooksPath is set (%s) — a hook manager may own it.\n", hooksDir)
		fmt.Println("Not writing there. Add these lines to your hook manager's config instead:")
		for _, h := range installer.Hooks {
			fmt.Printf("  %s: context-diary hook %s \"$@\"\n", h, h)
		}
	} else {
		results, err := installer.Install(hooksDir)
		if err != nil {
			warnf("install hooks: %v", err)
			return 1
		}
		printResults(results)
	}

	top, err := gitx.TopLevel(".")
	if err != nil {
		top = "."
	}
	created, err := installer.ScaffoldConfig(top)
	if err != nil {
		warnf("config scaffold: %v", err)
		return 1
	}
	if created {
		fmt.Println(".context-diary.toml:  created")
	} else {
		fmt.Println(".context-diary.toml:  already present")
	}

	if agent != "" {
		res, err := installer.AgentSetup(top, agent)
		if err != nil {
			warnf("agent setup: %v", err)
			return 1
		}
		printResults([]installer.Result{res})
	}
	return 0
}

func printResults(results []installer.Result) {
	for _, res := range results {
		fmt.Printf("%-20s %s\n", res.Hook+":", res.Status)
		if res.Instruction != "" {
			fmt.Println("  " + res.Instruction)
		}
	}
}

// cmdHook dispatches git hook entry points. Never-block invariant (I-1):
// prepare-commit-msg always exits 0; commit-msg exits 1 only in strict mode.
//
// @intent implement the git hook entry points (prepare-commit-msg, commit-msg) invoked by git
// @domainRule never-block (I-1): prepare-commit-msg always exits 0; commit-msg exits 1 only under strict lint
func cmdHook(args []string) int {
	if len(args) < 2 {
		warnf("hook: missing arguments")
		return 0
	}
	name, msgFile := args[0], args[1]

	cfg, err := loadConfig()
	if err != nil {
		warnf("config: %v (hook skipped)", err)
		return 0
	}

	switch name {
	case "prepare-commit-msg":
		source := ""
		if len(args) > 2 {
			source = args[2]
		}
		hook.Prepare(hook.Deps{
			Config:      cfg,
			StagedDiff:  func() (string, error) { return gitx.StagedDiff(".") },
			CommentChar: gitx.CommentChar("."),
			Stderr:      os.Stderr,
		}, msgFile, source)
		return 0
	case "commit-msg":
		raw, err := os.ReadFile(msgFile)
		if err != nil {
			warnf("read message: %v", err)
			return 0
		}
		res := hook.CommitMsg(cfg, string(raw), gitx.CommentChar("."))
		for _, v := range res.Violations {
			warnf("%s: %s", v.Code, v.Msg)
		}
		if res.Blocked {
			warnf("commit rejected (lint.level = strict); fix the trailers above and retry")
			return 1
		}
		return 0
	default:
		warnf("unknown hook %q", name)
		return 0
	}
}

// cmdLint validates the trailers of every commit in a revision range.
//
// @intent implement `context-diary lint <rev-range>`: the CI gate that fails when a commit lacks context trailers
// @ensures exit code 1 when any commit in the range has violations
func cmdLint(revRange string) int {
	commits, err := gitx.RevList(".", revRange)
	if err != nil {
		warnf("rev-list: %v", err)
		return 1
	}
	failed := false
	for _, c := range commits {
		msg, err := gitx.CommitMessage(".", c)
		if err != nil {
			warnf("%s: %v", c, err)
			failed = true
			continue
		}
		for _, v := range trailer.Lint(msg) {
			fmt.Printf("%s %s: %s\n", c[:min(12, len(c))], v.Code, v.Msg)
			failed = true
		}
	}
	if failed {
		return 1
	}
	fmt.Printf("%d commits clean\n", len(commits))
	return 0
}

// cmdScopes prints the configured scope slugs.
//
// @intent implement `context-diary scopes`: list the shared scope slugs from config
func cmdScopes() int {
	cfg, err := loadConfig()
	if err != nil {
		warnf("config: %v", err)
		return 1
	}
	for _, s := range cfg.Scopes {
		fmt.Println(s)
	}
	return 0
}
