package config

import (
	"os"
	"path/filepath"
	"testing"
)

func noEnv(string) string { return "" }

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsWhenNoFiles(t *testing.T) {
	cfg, err := Load("/nonexistent/repo.toml", "/nonexistent/user.toml", noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Hook.Mode != ModeComment {
		t.Errorf("default mode = %q, want comment", cfg.Hook.Mode)
	}
	if cfg.Lint.Level != LevelWarn {
		t.Errorf("default lint level = %q, want warn", cfg.Lint.Level)
	}
	if len(cfg.Scopes) != 0 {
		t.Errorf("default scopes = %v, want empty", cfg.Scopes)
	}
}

func TestLayering(t *testing.T) {
	dir := t.TempDir()
	user := write(t, dir, "user.toml", `
[hook]
mode = "off"
[lint]
level = "strict"
`)
	repo := write(t, dir, "repo.toml", `
scopes = ["order/cancel"]

[hook]
mode = "comment"
`)
	cfg, err := Load(repo, user, noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Hook.Mode != ModeComment {
		t.Errorf("mode = %q, want comment (repo overrides user)", cfg.Hook.Mode)
	}
	if cfg.Lint.Level != LevelStrict {
		t.Errorf("level = %q, want strict (user file kept, repo silent)", cfg.Lint.Level)
	}
	if len(cfg.Scopes) != 1 || cfg.Scopes[0] != "order/cancel" {
		t.Errorf("scopes = %v", cfg.Scopes)
	}

	env := func(k string) string {
		if k == "CONTEXT_DIARY_HOOK_MODE" {
			return "off"
		}
		return ""
	}
	cfg, err = Load(repo, user, env)
	if err != nil {
		t.Fatalf("Load with env: %v", err)
	}
	if cfg.Hook.Mode != ModeOff {
		t.Errorf("mode = %q, want off (env beats files)", cfg.Hook.Mode)
	}
}

func TestInvalidInput(t *testing.T) {
	dir := t.TempDir()
	broken := write(t, dir, "broken.toml", "[hook\nmode=")
	if _, err := Load(broken, "/nonexistent", noEnv); err == nil {
		t.Error("Load(broken toml) = nil error, want error")
	}

	badMode := write(t, dir, "badmode.toml", "[hook]\nmode = \"fill\"\n")
	if _, err := Load(badMode, "/nonexistent", noEnv); err == nil {
		t.Error("Load(removed fill mode) = nil error, want error")
	}

	badLevel := write(t, dir, "badlevel.toml", "[lint]\nlevel = \"blocking\"\n")
	if _, err := Load(badLevel, "/nonexistent", noEnv); err == nil {
		t.Error("Load(bad lint level) = nil error, want error")
	}
}
