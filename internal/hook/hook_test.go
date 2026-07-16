package hook

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tae2089/context-diary/internal/config"
)

func baseConfig() config.Config {
	cfg, err := config.Load("/nonexistent", "/nonexistent", func(string) string { return "" })
	if err != nil {
		panic(err)
	}
	return cfg
}

func writeMsg(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readBack(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func prepDeps(cfg config.Config) Deps {
	return Deps{
		Config:      cfg,
		StagedDiff:  func() (string, error) { return "x | 1 +\n1 file changed", nil },
		CommentChar: "#",
		Stderr:      &strings.Builder{},
	}
}

// runPrepare runs Prepare and asserts whether the message file changed.
func runPrepare(t *testing.T, deps Deps, msg, source string, wantChanged bool) string {
	t.Helper()
	path := writeMsg(t, msg)
	Prepare(deps, path, source)
	got := readBack(t, path)
	if changed := got != msg; changed != wantChanged {
		t.Errorf("file changed = %v, want %v; content:\n%s", changed, wantChanged, got)
	}
	return got
}

func TestPrepareSkipPaths(t *testing.T) {
	msg := "subject\n\nbody\n"
	cfg := baseConfig()

	offCfg := cfg
	offCfg.Hook.Mode = config.ModeOff
	runPrepare(t, prepDeps(offCfg), msg, "", false)

	runPrepare(t, prepDeps(cfg), msg, "merge", false)
	runPrepare(t, prepDeps(cfg), msg, "squash", false)
	runPrepare(t, prepDeps(cfg), msg, "message", false) // -m: editor never opens

	withWhy := "subject\n\nContext-Why: already here\n"
	runPrepare(t, prepDeps(cfg), withWhy, "", false)

	// a previously injected *comment* template is not "already accepted"
	withDraft := "subject\n\n# Context-Why: draft only\n"
	runPrepare(t, prepDeps(cfg), withDraft, "", true)

	empty := prepDeps(cfg)
	empty.StagedDiff = func() (string, error) { return "", nil }
	runPrepare(t, empty, msg, "", false)

	failing := prepDeps(cfg)
	failing.StagedDiff = func() (string, error) { return "", errors.New("boom") }
	runPrepare(t, failing, msg, "", false) // I-1: warn, leave untouched
}

func TestPrepareInjectsCommentedTemplate(t *testing.T) {
	got := runPrepare(t, prepDeps(baseConfig()), "subject\n\nbody\n", "", true)
	if !strings.Contains(got, "# Context-Why: ") || !strings.Contains(got, "# Context-Scope: ") {
		t.Errorf("no commented template stubs:\n%s", got)
	}
}

func TestPrepareRespectsCommentChar(t *testing.T) {
	deps := prepDeps(baseConfig())
	deps.CommentChar = ";"
	got := runPrepare(t, deps, "subject\n\nbody\n", "", true)
	if !strings.Contains(got, "; Context-Why: ") {
		t.Errorf("custom commentChar not used:\n%s", got)
	}
}

func TestPrepareInsertsBeforeGitCommentBlock(t *testing.T) {
	msg := "subject\n\n# Please enter the commit message...\n# Changes to be committed:\n"
	got := runPrepare(t, prepDeps(baseConfig()), msg, "", true)
	tmplIdx := strings.Index(got, "# Context-Why: ")
	gitIdx := strings.Index(got, "# Please enter")
	if tmplIdx == -1 || gitIdx == -1 || tmplIdx > gitIdx {
		t.Errorf("template not inserted before git comment block:\n%s", got)
	}
}

func TestCommitMsg(t *testing.T) {
	cfg := baseConfig()

	clean := "subject\n\nContext-Why: fine\n"
	if res := CommitMsg(cfg, clean, "#"); res.Blocked || len(res.Violations) != 0 {
		t.Errorf("clean message: %+v", res)
	}

	dirty := "subject\n\nbody without trailers\n"
	res := CommitMsg(cfg, dirty, "#")
	if res.Blocked {
		t.Error("warn level must not block")
	}
	if len(res.Violations) == 0 {
		t.Error("expected violations at warn level")
	}

	cfg.Lint.Level = config.LevelStrict
	if res := CommitMsg(cfg, dirty, "#"); !res.Blocked {
		t.Error("strict level must block on violations")
	}

	// unaccepted comment templates are not accepted content
	commented := "subject\n\n# Context-Why: draft\n"
	if res := CommitMsg(cfg, commented, "#"); !res.Blocked {
		t.Error("commented template should still count as missing Context-Why")
	}
}
