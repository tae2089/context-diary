package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallIntoEmptySlots(t *testing.T) {
	dir := t.TempDir()
	results, err := Install(dir)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for _, res := range results {
		if res.Status != StatusInstalled {
			t.Errorf("%s: status %s, want installed", res.Hook, res.Status)
		}
		path := filepath.Join(dir, res.Hook)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("%s not executable", res.Hook)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), Marker) {
			t.Errorf("%s missing marker", res.Hook)
		}
		if !strings.Contains(string(b), "context-diary hook "+res.Hook) {
			t.Errorf("%s does not delegate to binary:\n%s", res.Hook, b)
		}
	}
}

func TestReinstallIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	results, err := Install(dir)
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	for _, res := range results {
		if res.Status != StatusUpdated {
			t.Errorf("%s: status %s, want updated", res.Hook, res.Status)
		}
	}
}

func TestForeignHookUntouched(t *testing.T) {
	dir := t.TempDir()
	foreign := "#!/bin/sh\n# husky\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "prepare-commit-msg"), []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	results, err := Install(dir)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	var prepare Result
	for _, res := range results {
		if res.Hook == "prepare-commit-msg" {
			prepare = res
		}
	}
	if prepare.Status != StatusManual {
		t.Errorf("status %s, want manual", prepare.Status)
	}
	if !strings.Contains(prepare.Instruction, "context-diary hook prepare-commit-msg") {
		t.Errorf("instruction missing manual line: %q", prepare.Instruction)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "prepare-commit-msg"))
	if string(b) != foreign {
		t.Error("foreign hook was modified")
	}
}

func TestScaffoldConfigOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".context-diary.toml")

	created, err := ScaffoldConfig(dir)
	if err != nil || !created {
		t.Fatalf("ScaffoldConfig: created=%v err=%v", created, err)
	}
	if err := os.WriteFile(path, []byte("# user edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err = ScaffoldConfig(dir)
	if err != nil || created {
		t.Fatalf("second ScaffoldConfig: created=%v err=%v (must not overwrite)", created, err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "# user edited\n" {
		t.Error("existing config was overwritten")
	}
}
