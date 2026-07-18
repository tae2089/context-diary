// Package config loads context-diary configuration with the precedence
// defined in docs/cli-design.md: env > repo file > user file > defaults.
//
// @index Loads context-diary hook/lint configuration with env-over-repo-over-user-over-default precedence; secrets never here.
package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Hook modes.
const (
	ModeComment = "comment"
	ModeOff     = "off"
)

// Lint levels.
const (
	LevelWarn   = "warn"
	LevelStrict = "strict"
)

// Config is the resolved configuration.
type Config struct {
	Hook struct {
		Mode string
	}
	Lint struct {
		Level string
	}
	Scopes []string
}

// fileConfig mirrors the TOML shape with pointer fields so that layering can
// distinguish "unset" from zero values.
type fileConfig struct {
	Hook *struct {
		Mode *string `toml:"mode"`
	} `toml:"hook"`
	Lint *struct {
		Level *string `toml:"level"`
	} `toml:"lint"`
	Scopes *[]string `toml:"scopes"`
}

// Load resolves configuration from the repo file, the user file, env, and
// defaults. Missing files are fine; unreadable or invalid content is an error
// (the hook caller downgrades errors to warnings per the never-block rule).
func Load(repoFile, userFile string, getenv func(string) string) (Config, error) {
	var cfg Config
	cfg.Hook.Mode = ModeComment
	cfg.Lint.Level = LevelWarn

	for _, path := range []string{userFile, repoFile} {
		if err := applyFile(&cfg, path); err != nil {
			return Config{}, err
		}
	}

	if v := getenv("CONTEXT_DIARY_HOOK_MODE"); v != "" {
		cfg.Hook.Mode = v
	}
	if v := getenv("CONTEXT_DIARY_LINT_LEVEL"); v != "" {
		cfg.Lint.Level = v
	}

	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var fc fileConfig
	if err := toml.Unmarshal(data, &fc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if fc.Hook != nil && fc.Hook.Mode != nil {
		cfg.Hook.Mode = *fc.Hook.Mode
	}
	if fc.Lint != nil && fc.Lint.Level != nil {
		cfg.Lint.Level = *fc.Lint.Level
	}
	if fc.Scopes != nil {
		cfg.Scopes = *fc.Scopes
	}
	return nil
}

func validate(cfg Config) error {
	switch cfg.Hook.Mode {
	case ModeComment, ModeOff:
	default:
		return fmt.Errorf("invalid hook.mode %q (want comment or off)", cfg.Hook.Mode)
	}
	switch cfg.Lint.Level {
	case LevelWarn, LevelStrict:
	default:
		return fmt.Errorf("invalid lint.level %q (want warn or strict)", cfg.Lint.Level)
	}
	return nil
}
