package sweep

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// staleChatDays: AI chat history older than this is offered (never recommended:
// it's the user's history, and some tools use it for /resume or memory).
const staleChatDays = 30

// ScanAIData finds the mess AI tools and editors leave behind: old chat
// history, chats for folders that no longer exist, and app caches.
func ScanAIData(e *Env) []Item {
	var items []Item
	items = append(items, claudeCodeChats(e)...)
	items = append(items, agentChats(e)...)
	items = append(items, claudeCodeTemp(e)...)
	items = append(items, editorWorkspaces(e)...)
	items = append(items, appCaches(e)...)
	return items
}

var cwdRe = regexp.MustCompile(`"cwd":"((?:[^"\\]|\\.)*)"`)

// sessionCwd reads the working folder a Claude Code chat was started in.
func sessionCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf, _ := io.ReadAll(io.LimitReader(f, 256<<10))
	m := cwdRe.FindSubmatch(buf)
	if m == nil {
		return ""
	}
	var s string
	if json.Unmarshal([]byte(`"`+string(m[1])+`"`), &s) != nil {
		return ""
	}
	return s
}

// claudeCodeChats offers Claude Code chat files (~/.claude/projects/<proj>/<id>.jsonl
// plus the matching <id>/ folder) for deleted folders or older than staleChatDays.
// Memory folders are never touched.
func claudeCodeChats(e *Env) []Item {
	root := filepath.Join(e.Home, ".claude", "projects")
	projects, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	type group struct {
		paths    []string
		chats    int
		projects map[string]bool
		last     time.Time
	}
	orphan := &group{projects: map[string]bool{}}
	old := &group{projects: map[string]bool{}}
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		dir := filepath.Join(root, p.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, en := range entries {
			name := en.Name()
			if en.IsDir() || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			info, err := en.Info()
			if err != nil {
				continue
			}
			full := filepath.Join(dir, name)
			var g *group
			if cwd := sessionCwd(full); cwd != "" && !Exists(cwd) {
				g = orphan
			} else if DaysSince(info.ModTime(), e.Now) > staleChatDays {
				g = old
			} else {
				continue
			}
			g.paths = append(g.paths, full)
			if side := strings.TrimSuffix(full, ".jsonl"); IsDir(side) {
				g.paths = append(g.paths, side)
			}
			g.chats++
			g.projects[p.Name()] = true
			if info.ModTime().After(g.last) {
				g.last = info.ModTime()
			}
		}
	}
	var items []Item
	if orphan.chats > 0 {
		items = append(items, Item{
			Category: CatAIData, Title: fmt.Sprintf("Claude Code chats for %d deleted folders", len(orphan.projects)),
			Paths: orphan.paths, LastUsed: orphan.last, Verdict: VerdictProbably,
			Detail: fmt.Sprintf("%d chats whose project folder is gone · memory notes are kept", orphan.chats),
		})
	}
	if old.chats > 0 {
		items = append(items, Item{
			Category: CatAIData, Title: fmt.Sprintf("Claude Code chats older than %d days", staleChatDays),
			Paths: old.paths, LastUsed: old.last,
			Detail: fmt.Sprintf("%d chats · /resume can't open them after · memory notes are kept", old.chats),
		})
	}
	return items
}

// staleFiles lists files under root not modified for days (up to a limit).
func staleFiles(root string, days int, now time.Time) (paths []string, last time.Time) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&fs.ModeSymlink != 0 || len(paths) >= 20000 {
			return nil
		}
		info, err := d.Info()
		if err != nil || DaysSince(info.ModTime(), now) <= days {
			return nil
		}
		paths = append(paths, p)
		if info.ModTime().After(last) {
			last = info.ModTime()
		}
		return nil
	})
	return
}

// agentChats covers other coding agents' chat logs.
func agentChats(e *Env) []Item {
	type src struct{ name, dir string }
	var items []Item
	for _, s := range []src{
		{"Codex", filepath.Join(e.Home, ".codex", "sessions")},
		{"Gemini CLI", filepath.Join(e.Home, ".gemini", "tmp")},
	} {
		if !IsDir(s.dir) {
			continue
		}
		paths, last := staleFiles(s.dir, staleChatDays, e.Now)
		if len(paths) == 0 {
			continue
		}
		items = append(items, Item{
			Category: CatAIData, Title: fmt.Sprintf("%s chats older than %d days", s.name, staleChatDays),
			Paths: paths, LastUsed: last,
			Detail: fmt.Sprintf("%d files in %s · old chat history", len(paths), tildePath(e.Home, s.dir)),
		})
	}
	return items
}

// claudeCodeTemp: shell snapshots, file-history (rewind), paste cache, debug logs.
func claudeCodeTemp(e *Env) []Item {
	var paths []string
	var last time.Time
	for _, d := range []string{"shell-snapshots", "file-history", "paste-cache", "debug", "todos"} {
		p, l := staleFiles(filepath.Join(e.Home, ".claude", d), staleChatDays, e.Now)
		paths = append(paths, p...)
		if l.After(last) {
			last = l
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return []Item{{
		Category: CatAIData, Title: "Claude Code temp files", Paths: paths, LastUsed: last, Recommended: true, Regenerable: true,
		Detail: fmt.Sprintf("%d old shell snapshots, rewind copies, debug logs · older than %d days", len(paths), staleChatDays),
	}}
}

type editor struct {
	name, support, process string
}

var editors = []editor{
	{"Claude app", "Claude", "Claude.app/Contents/"},
	{"Cursor", "Cursor", "Cursor.app/Contents/"},
	{"VS Code", "Code", "Visual Studio Code.app/Contents/"},
	{"Windsurf", "Windsurf", "Windsurf.app/Contents/"},
}

func appRunning(pattern string) bool {
	return len(strings.TrimSpace(string(Run(5*time.Second, "pgrep", "-f", pattern)))) > 0
}

// editorWorkspaces: per-folder state (including Cursor's AI chats) for
// folders that no longer exist.
func editorWorkspaces(e *Env) []Item {
	var items []Item
	for _, ed := range editors {
		root := filepath.Join(e.Home, "Library", "Application Support", ed.support, "User", "workspaceStorage")
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		var paths []string
		var last time.Time
		for _, en := range entries {
			dir := filepath.Join(root, en.Name())
			b, err := os.ReadFile(filepath.Join(dir, "workspace.json"))
			if err != nil {
				continue
			}
			var ws struct{ Folder, Workspace string }
			if json.Unmarshal(b, &ws) != nil {
				continue
			}
			target := ws.Folder
			if target == "" {
				target = ws.Workspace
			}
			u, err := url.Parse(target)
			if err != nil || u.Scheme != "file" || Exists(u.Path) {
				continue // remote, unknown or still there
			}
			paths = append(paths, dir)
			if t := NewestMtime(dir); t.After(last) {
				last = t
			}
		}
		if len(paths) == 0 {
			continue
		}
		items = append(items, Item{
			Category: CatAIData, Title: fmt.Sprintf("%s state for %d deleted folders", ed.name, len(paths)),
			Paths: paths, LastUsed: last, Verdict: VerdictProbably,
			Detail: "per-project editor state and AI chats for folders that no longer exist",
		})
	}
	return items
}

// appCaches: Chromium/Electron caches of AI apps and editors. Only offered
// while the app is closed.
func appCaches(e *Env) []Item {
	var items []Item
	for _, ed := range editors {
		base := filepath.Join(e.Home, "Library", "Application Support", ed.support)
		var paths []string
		for _, d := range []string{"Cache", "Code Cache", "GPUCache", "DawnGraphiteCache", "DawnWebGPUCache",
			"GraphiteDawnCache", "CachedData", "CachedExtensionVSIXs", "CachedProfilesData", "logs"} {
			if p := filepath.Join(base, d); IsDir(p) {
				paths = append(paths, p)
			}
		}
		if len(paths) == 0 {
			continue
		}
		it := Item{
			Category: CatAIData, Title: ed.name + " cache", Paths: paths, Regenerable: true,
			Detail: tildePath(e.Home, base) + " · rebuilt when the app starts",
		}
		if appRunning(ed.process) {
			it.ReadOnly = true
			it.Verdict = "quit it first"
			it.Detail = ed.name + " is open · quit it, then scan again"
		} else {
			it.Recommended = true
		}
		items = append(items, it)
	}
	return items
}
