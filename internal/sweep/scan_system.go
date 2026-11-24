package sweep

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type cacheSpec struct {
	rel, title, detail string
	recommended        bool
}

var cacheSpecs = []cacheSpec{
	{".npm/_cacache", "npm cache", "re-downloads packages on next install", true},
	{"Library/Caches/Yarn", "Yarn cache", "re-downloads on next install", true},
	{".yarn/berry/cache", "Yarn Berry cache", "re-downloads on next install", true},
	{".bun/install/cache", "Bun cache", "re-downloads on next install", true},
	{".cache/uv", "uv cache", "re-downloads Python packages when needed", true},
	{"Library/Caches/pip", "pip cache", "re-downloads Python packages when needed", true},
	{"Library/Caches/pypoetry", "Poetry cache", "re-downloads when needed", true},
	{"Library/Caches/Homebrew", "Homebrew downloads", "old bottles and installers", true},
	{"Library/Caches/CocoaPods", "CocoaPods cache", "pod install re-downloads", true},
	{"Library/Caches/org.swift.swiftpm", "SwiftPM cache", "re-downloads packages", true},
	{"Library/Caches/go-build", "Go build cache", "rebuilds itself", true},
	{"Library/Developer/CoreSimulator/Caches", "Simulator caches", "rebuilds itself", true},
	{".cargo/registry", "Cargo registry", "re-downloads crates on next build", false},
	{"go/pkg/mod", "Go module cache", "re-downloads modules on next build", false},
	{".pub-cache", "Dart pub cache", "run `flutter pub get` again in each project after", false},
	{"Library/Caches/ms-playwright", "Playwright browsers", "`npx playwright install` brings them back", false},
	{"Library/Application Support/Claude/vm_bundles", "Claude Cowork VM", "re-downloads next time you use Cowork", false},
	{".cache/pre-commit", "pre-commit envs", "rebuilt on next commit", true},
	{".cache/puppeteer", "Puppeteer browsers", "re-downloads on next install", false},
}

// ScanCaches lists package-manager and tool caches.
func ScanCaches(e *Env) []Item {
	var items []Item
	for _, c := range cacheSpecs {
		p := filepath.Join(e.Home, c.rel)
		if !IsDir(p) {
			continue
		}
		items = append(items, Item{
			Category: CatCaches, Title: c.title, Paths: []string{p}, Regenerable: true, Recommended: c.recommended,
			Detail: tildePath(e.Home, p) + " · " + c.detail,
		})
	}
	// App updater leftovers (*.ShipIt) are dead weight once the update installed.
	if ship, _ := filepath.Glob(filepath.Join(e.Home, "Library/Caches/*.ShipIt")); len(ship) > 0 {
		items = append(items, Item{
			Category: CatCaches, Title: "App updater leftovers", Paths: ship, Regenerable: true, Recommended: true,
			Detail: fmt.Sprintf("%d *.ShipIt folders from finished app updates", len(ship)),
		})
	}
	if _, err := exec.LookPath("pnpm"); err == nil {
		if out := bytes.TrimSpace(Run(20*time.Second, "pnpm", "store", "path")); len(out) > 0 && IsDir(string(out)) {
			items = append(items, Item{
				ID:       NewID(CatCaches, "pnpm-store"),
				Category: CatCaches, Title: "pnpm store (unreferenced packages)", Paths: []string{string(out)},
				Detail:  "runs `pnpm store prune` · size shown is the whole store",
				Command: []string{"pnpm", "store", "prune"},
			})
		}
	}
	return items
}

// ScanDocker reports what Docker could reclaim.
func ScanDocker(e *Env) []Item {
	out := Run(20*time.Second, "docker", "system", "df", "--format", "{{json .}}")
	if len(out) == 0 {
		raw := filepath.Join(e.Home, "Library/Containers/com.docker.docker/Data/vms/0/data/Docker.raw")
		if Exists(raw) {
			return []Item{{
				Category: CatInfo, Title: "Docker disk image", Paths: []string{raw}, ReadOnly: true, Verdict: "start Docker",
				Detail: "Docker isn't running · start it and run buybytes again to see what's reclaimable",
			}}
		}
		return nil
	}
	var total int64
	var parts []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		var row struct{ Type, Reclaimable string }
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		f := strings.Fields(row.Reclaimable)
		if len(f) == 0 {
			continue
		}
		if b := parseDockerSize(f[0]); b > 0 {
			total += b
			parts = append(parts, fmt.Sprintf("%s %s", row.Type, HumanSize(b)))
		}
	}
	if total == 0 {
		return nil
	}
	return []Item{{
		ID:       NewID(CatCaches, "docker"),
		Category: CatCaches, Title: "Docker: unused images, containers, build cache", Size: total,
		Detail:  strings.Join(parts, ", ") + " · runs `docker system prune -a`",
		Command: []string{"docker", "system", "prune", "-a", "-f"},
	}}
}

func parseDockerSize(s string) int64 {
	units := []struct {
		suf  string
		mult float64
	}{{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suf) {
			v, err := strconv.ParseFloat(strings.TrimSuffix(s, u.suf), 64)
			if err != nil {
				return 0
			}
			return int64(v * u.mult)
		}
	}
	return 0
}

// ScanBackups shows iPhone/iPad backups. They are user data: read-only, never selectable.
func ScanBackups(e *Env) []Item {
	dir := filepath.Join(e.Home, "Library/Application Support/MobileSync/Backup")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return nil
	}
	var paths []string
	for _, en := range entries {
		paths = append(paths, filepath.Join(dir, en.Name()))
	}
	return []Item{{
		Category: CatInfo, Title: fmt.Sprintf("%d iPhone/iPad backups", len(paths)), Paths: paths, ReadOnly: true, Verdict: "use Finder",
		LastUsed: NewestMtime(dir),
		Detail:   "your device data · manage in Finder › your device › Manage Backups",
	}}
}

// ScanWorktrees finds git worktrees left behind by coding agents and parallel
// work. Only clean worktrees (git reports no changes) can be selected.
func ScanWorktrees(e *Env) []Item {
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	var mu sync.Mutex
	var items []Item
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, p := range e.Projects {
		if !IsDir(filepath.Join(p.Path, ".git")) {
			continue // worktrees themselves have a .git file; list from the main repo only
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(repo string) {
			defer wg.Done()
			defer func() { <-sem }()
			found := repoWorktrees(e, repo)
			mu.Lock()
			items = append(items, found...)
			mu.Unlock()
		}(p.Path)
	}
	wg.Wait()
	return items
}

func repoWorktrees(e *Env, repo string) []Item {
	out := Run(15*time.Second, "git", "-C", repo, "worktree", "list", "--porcelain")
	if len(out) == 0 {
		return nil
	}
	type wt struct {
		path, branch string
		prunable     bool
	}
	var wts []wt
	for _, block := range strings.Split(strings.TrimSpace(string(out)), "\n\n") {
		var w wt
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				w.path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "branch "):
				w.branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
			case strings.HasPrefix(line, "prunable"):
				w.prunable = true
			}
		}
		wts = append(wts, w)
	}
	if len(wts) < 2 {
		return nil
	}
	mainBranch := wts[0].branch
	merged := map[string]bool{}
	if mainBranch != "" {
		for _, l := range strings.Split(string(Run(15*time.Second, "git", "-C", repo, "branch", "--merged", mainBranch, "--format=%(refname:short)")), "\n") {
			merged[strings.TrimSpace(l)] = true
		}
	}
	var items []Item
	for _, w := range wts[1:] {
		if w.prunable || !IsDir(w.path) {
			continue
		}
		last := projectActivity(w.path, nil)
		it := Item{
			ID:       NewID(CatAgents, "wt:"+w.path),
			Category: CatAgents, Title: tildePath(e.Home, w.path), Paths: []string{w.path}, LastUsed: last,
			Command: []string{"git", "-C", repo, "worktree", "remove", w.path},
		}
		status := Run(15*time.Second, "git", "-C", w.path, "status", "--porcelain")
		branch := w.branch
		if branch == "" {
			branch = "detached HEAD"
		}
		switch {
		case len(bytes.TrimSpace(status)) > 0:
			it.ReadOnly = true
			it.Verdict = "commit first"
			it.Detail = fmt.Sprintf("%s · has uncommitted changes, commit or stash first", branch)
		case merged[w.branch] && w.branch != "":
			it.Recommended = true
			it.Detail = fmt.Sprintf("%s is merged into %s · clean", branch, mainBranch)
		case DaysSince(last, e.Now) > untouchedDays:
			it.Recommended = true
			it.Detail = fmt.Sprintf("%s · clean · untouched, branch is kept", branch)
		default:
			it.Detail = fmt.Sprintf("%s · clean · branch is kept", branch)
		}
		items = append(items, it)
	}
	return items
}
