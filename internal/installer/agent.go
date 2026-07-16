package installer

import (
	"fmt"
	"os"
	"path/filepath"
)

// instructions teaches an AI coding agent the trailer convention. Keep in
// sync with docs/trailer-format.md (design R4: versioned marker below).
const instructions = `## Commit context trailers (context-diary format v0.1)

Every commit you author must carry context trailers in the last paragraph of
the commit message:

- ` + "`Context-Why:`" + ` (required, once) — one line on why this change exists: the
  problem or motivation, not a restatement of what changed.
- ` + "`Context-Scope:`" + ` (optional, repeatable) — product-concept slug the change
  belongs to: lowercase segments joined by "/", e.g. ` + "`order/cancel`" + `. Prefer
  slugs from the repo's .context-diary.toml scopes list.
- ` + "`Context-Decision:`" + ` (optional, repeatable) — notable tradeoff, ideally
  shaped as "chosen over rejected; reason".
- ` + "`Context-Ref:`" + ` (optional, repeatable) — URL or ID of a related issue,
  ADR, incident, or doc.

Every value fits on one line; put longer explanation in the commit body
above the trailer block. Write at developer language level.
`

// Instructions returns the agent convention snippet.
func Instructions() string { return instructions }

// AgentFile maps an agent name to its convention file.
func AgentFile(agent string) (string, error) {
	switch agent {
	case "claude-code":
		return "CLAUDE.md", nil
	case "codex":
		return "AGENTS.md", nil
	default:
		return "", fmt.Errorf("unknown agent %q (supported: claude-code, codex)", agent)
	}
}

// AgentSetup creates the agent's convention file with the instructions
// snippet when absent. An existing file is never modified (design I-3);
// the snippet is returned as a manual instruction instead.
func AgentSetup(dir, agent string) (Result, error) {
	file, err := AgentFile(agent)
	if err != nil {
		return Result{}, err
	}
	path := filepath.Join(dir, file)
	if _, err := os.Stat(path); err == nil {
		return Result{
			Hook:   file,
			Status: StatusManual,
			Instruction: fmt.Sprintf(
				"%s already exists; add this section to it manually:\n\n%s", file, instructions),
		}, nil
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	if err := os.WriteFile(path, []byte(instructions), 0o644); err != nil {
		return Result{}, fmt.Errorf("write %s: %w", path, err)
	}
	return Result{Hook: file, Status: StatusInstalled}, nil
}
