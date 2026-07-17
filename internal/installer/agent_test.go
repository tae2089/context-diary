package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstructionsPerAgent(t *testing.T) {
	for agent, wantFile := range map[string]string{
		"claude-code": "CLAUDE.md",
		"codex":       "AGENTS.md",
	} {
		file, err := AgentFile(agent)
		if err != nil {
			t.Fatalf("AgentFile(%s): %v", agent, err)
		}
		if file != wantFile {
			t.Errorf("AgentFile(%s) = %s, want %s", agent, file, wantFile)
		}
	}
	if _, err := AgentFile("cursor"); err == nil {
		t.Error("AgentFile(unknown) should error")
	}

	snippet := Instructions()
	for _, want := range []string{
		"Context-Why", "Context-Scope", "Context-Decision", "last paragraph",
		"Good:", "Bad", "double refunds", "fix bug", // quality examples
	} {
		if !strings.Contains(snippet, want) {
			t.Errorf("Instructions() missing %q", want)
		}
	}
}

func TestAgentSetupCreatesFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	res, err := AgentSetup(dir, "claude-code")
	if err != nil {
		t.Fatalf("AgentSetup: %v", err)
	}
	if res.Status != StatusInstalled {
		t.Errorf("status = %s, want installed", res.Status)
	}
	b, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Context-Why") {
		t.Errorf("created file missing instructions:\n%s", b)
	}
}

func TestAgentSetupNeverEditsExistingFile(t *testing.T) {
	dir := t.TempDir()
	existing := "# My rules\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := AgentSetup(dir, "codex")
	if err != nil {
		t.Fatalf("AgentSetup: %v", err)
	}
	if res.Status != StatusManual {
		t.Errorf("status = %s, want manual", res.Status)
	}
	if !strings.Contains(res.Instruction, "Context-Why") {
		t.Error("manual instruction should carry the snippet")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if string(b) != existing {
		t.Error("existing agent file was modified")
	}
}
