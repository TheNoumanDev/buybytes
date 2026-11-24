package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed skills/buybytes/SKILL.md
var skillMD []byte

// runSkill installs the agent skill into every agent folder that exists:
// Claude Code (~/.claude/skills), Codex (~/.codex/skills) and the shared
// ~/.agents/skills folder other agents read.
func runSkill(home string, args []string) {
	if len(args) == 0 || args[0] != "install" {
		fmt.Println("usage: buybytes skill install")
		return
	}
	targets := []struct{ agent, parent, dir string }{
		{"Claude Code", ".claude", ".claude/skills/buybytes"},
		{"Codex", ".codex", ".codex/skills/buybytes"},
		{"other agents", ".agents", ".agents/skills/buybytes"},
	}
	n := 0
	for _, t := range targets {
		if t.agent != "Claude Code" && !isDir(filepath.Join(home, t.parent)) {
			continue
		}
		dir := filepath.Join(home, t.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", t.agent, err)
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), skillMD, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", t.agent, err)
			continue
		}
		fmt.Printf("  ✓ %s: ~/%s/SKILL.md\n", t.agent, t.dir)
		n++
	}
	if n > 0 {
		fmt.Println("\nAsk your agent \"my disk is full\" or \"clean up my Mac\" in a new session.")
	}
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
