package sweep

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mkfile(t *testing.T, p string, size int64, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if size > 0 {
		if err := f.Truncate(size); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	old := time.Now().Add(-age)
	_ = os.Chtimes(p, old, old)
}

func age(t *testing.T, p string, d time.Duration) {
	t.Helper()
	old := time.Now().Add(-d)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
}

const day = 24 * time.Hour

func TestCheckSafe(t *testing.T) {
	home := "/Users/x"
	bad := []string{
		"/Users/x", "/Users/x/Documents", "/Users/x/Downloads", "/Users/x/.claude", "/Users/x/.claude/skills/foo",
		"/Users/x/.ssh/id_rsa", "/etc", "/Users/y/build", "relative/path", "/Users/x/a/../Documents",
		"/Users/x/Library/Application Support/MobileSync/Backup/abc", "/Users/x/.agents/skills/thing",
		"/Users/x/.claude/projects", "/Users/x/.claude/projects/-Users-x-app", "/Users/x/.claude/projects/-Users-x-app/memory",
		"/Users/x/.claude/projects/-Users-x-app/memory/MEMORY.md",
	}
	for _, p := range bad {
		if CheckSafe(home, p) == nil {
			t.Errorf("CheckSafe(%q) allowed a protected path", p)
		}
	}
	good := []string{"/Users/x/Development/app/build", "/Users/x/Downloads/movie.mkv", "/Users/x/.gradle/caches/8.11",
		"/Users/x/.claude/projects/-Users-x-app/0c36.jsonl", "/Users/x/.claude/projects/-Users-x-app/0c36"}
	for _, p := range good {
		if err := CheckSafe(home, p); err != nil {
			t.Errorf("CheckSafe(%q) = %v, want ok", p, err)
		}
	}
}

func TestProjectArtifactsAndRemove(t *testing.T) {
	home := t.TempDir()
	stale := filepath.Join(home, "Development/old_app")
	active := filepath.Join(home, "Development/new_app")
	mkfile(t, filepath.Join(stale, "pubspec.yaml"), 10, 40*day)
	mkfile(t, filepath.Join(stale, "build/app.apk"), 4096, 40*day)
	mkfile(t, filepath.Join(stale, ".dart_tool/cache"), 4096, 40*day)
	mkfile(t, filepath.Join(stale, "lib/main.dart"), 10, 40*day)
	age(t, filepath.Join(stale, "lib"), 40*day)
	age(t, stale, 40*day)
	mkfile(t, filepath.Join(active, "package.json"), 10, 0)
	mkfile(t, filepath.Join(active, "node_modules/x/index.js"), 4096, 0)

	e := NewEnv(home, 0, nil)
	items := ScanProjectArtifacts(e)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(items), items)
	}
	var old, cur Item
	for _, it := range items {
		if it.Title == "~/Development/old_app" {
			old = it
		} else {
			cur = it
		}
	}
	if !old.Recommended || len(old.Paths) != 2 {
		t.Errorf("stale project: recommended=%v paths=%v, want recommended with build + .dart_tool", old.Recommended, old.Paths)
	}
	if cur.Recommended {
		t.Errorf("active project's node_modules should be the user's call")
	}

	// Trash mode moves into ~/.Trash and leaves source code alone.
	r := Remove(home, old, false, false)
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if Exists(filepath.Join(stale, "build")) || !Exists(filepath.Join(home, ".Trash/build")) {
		t.Error("build folder was not moved to the Trash")
	}
	if !Exists(filepath.Join(stale, "lib/main.dart")) || !Exists(filepath.Join(stale, "pubspec.yaml")) {
		t.Error("source files were touched")
	}
	// Emptying only touches what buybytes moved, nothing else in the Trash.
	mkfile(t, filepath.Join(home, ".Trash/someone-elses.txt"), 10, 0)
	if err := EmptyFromTrash(home, r.Moved); err != nil || Exists(filepath.Join(home, ".Trash/build")) {
		t.Errorf("EmptyFromTrash: %v", err)
	}
	if !Exists(filepath.Join(home, ".Trash/someone-elses.txt")) {
		t.Error("EmptyFromTrash removed something buybytes didn't put there")
	}
	if EmptyFromTrash(home, []string{filepath.Join(home, ".Trash")}) == nil || EmptyFromTrash(home, []string{stale}) == nil {
		t.Error("EmptyFromTrash accepted a path outside the Trash")
	}
	// Purge deletes for real.
	r = Remove(home, cur, true, false)
	if r.Err != nil || Exists(filepath.Join(active, "node_modules")) {
		t.Errorf("purge failed: %v", r.Err)
	}
	// Dry run touches nothing.
	mkfile(t, filepath.Join(active, "node_modules/y"), 10, 0)
	if r := Remove(home, cur, true, true); r.Err != nil || !Exists(filepath.Join(active, "node_modules/y")) {
		t.Error("dry run removed files")
	}
	// Read-only items are refused.
	if r := Remove(home, Item{ReadOnly: true, Paths: []string{stale}}, true, false); r.Err == nil {
		t.Error("read-only item was removed")
	}
}

func TestFVMKeepsPinnedVersions(t *testing.T) {
	home := t.TempDir()
	mkfile(t, filepath.Join(home, "fvm/versions/3.27.0/bin/flutter"), 10, 0)
	mkfile(t, filepath.Join(home, "fvm/versions/3.35.4/bin/flutter"), 10, 0)
	mkfile(t, filepath.Join(home, "fvm/versions/3.38.1/bin/flutter"), 10, 0)
	if err := os.Symlink(filepath.Join(home, "fvm/versions/3.38.1"), filepath.Join(home, "fvm/default")); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(home, "Development/app")
	mkfile(t, filepath.Join(proj, "pubspec.yaml"), 10, 0)
	if err := os.WriteFile(filepath.Join(proj, ".fvmrc"), []byte(`{"flutter":"3.35.4"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	items := ScanFVM(NewEnv(home, 0, nil))
	if len(items) != 3 {
		t.Fatalf("want all 3 versions listed, got %+v", items)
	}
	for _, it := range items {
		pinned := it.Title != "Flutter 3.27.0 (fvm)"
		if it.Recommended == pinned || it.InUse != pinned {
			t.Errorf("%s: recommended=%v in_use=%v", it.Title, it.Recommended, it.InUse)
		}
	}
}

func TestDownloads(t *testing.T) {
	home := t.TempDir()
	dl := filepath.Join(home, "Downloads")
	mkfile(t, filepath.Join(dl, "Show.S01.1080p.WEB-DL/Show.S01E01.1080p.mkv"), 300<<20, 60*day)
	mkfile(t, filepath.Join(dl, "Show.S01.1080p.WEB-DL/Show.S01E02.1080p.mkv"), 300<<20, 60*day)
	mkfile(t, filepath.Join(dl, "Movie.2026.1080p.HEVC.mkv"), 900<<20, 60*day)
	mkfile(t, filepath.Join(dl, "app-release.apk"), 80<<20, 30*day)
	mkfile(t, filepath.Join(dl, "today.apk"), 80<<20, 0)
	mkfile(t, filepath.Join(dl, "notes.pdf"), 1<<20, 60*day)
	items := ScanDownloads(NewEnv(home, 0, nil))
	titles := map[string]Item{}
	for _, it := range items {
		titles[it.Title] = it
	}
	if _, ok := titles["📺 Show S01"]; !ok {
		t.Errorf("series folder not grouped: %v", keys(titles))
	}
	if _, ok := titles["🎬 Movie 2026"]; !ok {
		t.Errorf("movie not found: %v", keys(titles))
	}
	if it, ok := titles["1 APK / AAB / IPA test builds"]; !ok || !it.Recommended {
		t.Errorf("old apk not recommended: %v", keys(titles))
	}
	if it, ok := titles["1 recent APK / AAB / IPA test builds"]; !ok || it.Recommended {
		t.Errorf("recent apk should be the user's call: %v", keys(titles))
	}
	for _, it := range items {
		for _, p := range it.Paths {
			if filepath.Base(p) == "notes.pdf" {
				t.Error("small personal file was offered")
			}
		}
	}
}

func TestWorktreesOnlyCleanOnesSelectable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	repo := filepath.Join(home, "Development/repo")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	mkfile(t, filepath.Join(repo, "package.json"), 10, 0)
	git(repo, "init", "-q", "-b", "main")
	git(repo, "add", ".")
	git(repo, "commit", "-qm", "init")
	git(repo, "worktree", "add", "-q", "-b", "agent-done", filepath.Join(home, "Development/repo-wt-clean"))
	git(repo, "worktree", "add", "-q", "-b", "agent-wip", filepath.Join(home, "Development/repo-wt-dirty"))
	mkfile(t, filepath.Join(home, "Development/repo-wt-dirty/new.txt"), 10, 0)

	items := ScanWorktrees(NewEnv(home, 0, nil))
	if len(items) != 2 {
		t.Fatalf("want 2 worktrees, got %+v", items)
	}
	for _, it := range items {
		switch filepath.Base(it.Paths[0]) {
		case "repo-wt-clean":
			if it.ReadOnly || !it.Recommended {
				t.Errorf("clean merged worktree should be recommended: %+v", it)
			}
			if r := Remove(home, it, false, false); r.Err != nil || Exists(it.Paths[0]) {
				t.Errorf("git worktree remove failed: %v", r.Err)
			}
		case "repo-wt-dirty":
			if !it.ReadOnly {
				t.Errorf("dirty worktree must not be selectable: %+v", it)
			}
		}
	}
}

func TestShortName(t *testing.T) {
	cases := map[string]string{
		"Carrie.S01.1080p.WEB-DL.Hindi.5.1-English.5.1.ESub.x264-HDHub4u.Ms": "Carrie S01",
		"Mirzapur.The.Movie.2026.1080p.HEVC.mkv":                             "Mirzapur The Movie 2026",
		"Mike":                                                               "Mike",
	}
	for in, want := range cases {
		if got := shortName(in); got != want {
			t.Errorf("shortName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVersionLess(t *testing.T) {
	if !versionLess("3.9.0", "3.10.0") || versionLess("27.0", "26.5") || !versionLess("26.5", "27.0") {
		t.Error("versionLess compares numerically")
	}
}

func TestParseDockerSize(t *testing.T) {
	if parseDockerSize("1.5GB") != 1_500_000_000 || parseDockerSize("512kB") != 512_000 || parseDockerSize("0B") != 0 {
		t.Error("parseDockerSize")
	}
}

func keys(m map[string]Item) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestClaudeCodeChats(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, ".claude/projects/-gone")
	write := func(p, body string, a time.Duration) {
		mkfile(t, p, 0, 0)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		age(t, p, a)
	}
	gone := filepath.Join(home, "deleted-app")
	live := filepath.Join(home, "Development/app")
	mkfile(t, filepath.Join(live, "package.json"), 10, 0)
	write(filepath.Join(proj, "a.jsonl"), `{"cwd":"`+gone+`"}`, 0)
	mkfile(t, filepath.Join(proj, "a/subagents/x.jsonl"), 10, 0)
	write(filepath.Join(proj, "b.jsonl"), `{"cwd":"`+live+`"}`, 60*day)
	write(filepath.Join(proj, "c.jsonl"), `{"cwd":"`+live+`"}`, 0)
	write(filepath.Join(proj, "memory/MEMORY.md"), "notes", 90*day)

	items := claudeCodeChats(NewEnv(home, 0, nil))
	if len(items) != 2 {
		t.Fatalf("want deleted-folder + old groups, got %+v", items)
	}
	for _, it := range items {
		for _, p := range it.Paths {
			if filepath.Base(p) == "c.jsonl" || strings.Contains(p, "memory") {
				t.Errorf("offered %s", p)
			}
		}
		if r := Remove(home, it, false, false); r.Err != nil {
			t.Fatal(r.Err)
		}
	}
	if Exists(filepath.Join(proj, "a.jsonl")) || Exists(filepath.Join(proj, "a")) || Exists(filepath.Join(proj, "b.jsonl")) {
		t.Error("chats were not moved to the Trash")
	}
	if !Exists(filepath.Join(proj, "c.jsonl")) || !Exists(filepath.Join(proj, "memory/MEMORY.md")) {
		t.Error("recent chat or memory was touched")
	}
}

func TestTrashNeverOverwrites(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 3; i++ {
		p := filepath.Join(home, fmt.Sprintf("d%d/same.txt", i))
		mkfile(t, p, 10, 0)
		if _, err := toTrash(home, p); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(home, ".Trash"))
	if len(entries) != 3 {
		t.Errorf("want 3 separate files in the Trash, got %d", len(entries))
	}
}

func TestSharePostFitsATweetAndLeaksNoPaths(t *testing.T) {
	removed := []Item{
		{Category: CatDev, Title: "~/Development/secret-client/app", Size: 9 << 30, Paths: []string{"/Users/x/Development/secret-client/app/build"}},
		{Category: CatToolchain, Title: "Flutter 3.27.0 (fvm)", Size: 2 << 30},
	}
	c := Summarize(removed)
	for seed := int64(0); seed < 50; seed++ {
		p := SharePost(c, seed)
		if n := len([]rune(p)); n > 280 {
			t.Errorf("post is %d characters:\n%s", n, p)
		}
		if strings.Contains(p, "secret-client") || strings.Contains(p, "/Users") {
			t.Errorf("post leaks a path:\n%s", p)
		}
		if !strings.Contains(p, "freed 11.0 GB") || strings.Contains(p, "Biggest") {
			t.Errorf("post misses the totals:\n%s", p)
		}
	}
}

func TestAppBundlesAreTheOnlyThingOutsideHome(t *testing.T) {
	for _, p := range []string{"/Applications/Slack.app", "/Applications/Adobe Premiere Pro 2026/Adobe Premiere Pro 2026.app"} {
		if err := CheckSafe("/Users/x", p); err != nil {
			t.Errorf("%s should be removable: %v", p, err)
		}
	}
	for _, p := range []string{"/Applications", "/Applications/Utilities", "/Applications/Slack.app/Contents",
		"/Applications/a/b/C.app", "/Applications/X.app/Contents/Y.app", "/System/Applications/Mail.app", "/usr/local/bin"} {
		if CheckSafe("/Users/x", p) == nil {
			t.Errorf("%s must be refused", p)
		}
	}
}

func TestModes(t *testing.T) {
	if m, err := ParseMode(""); err != nil || m != ModeDev {
		t.Error("default mode should be dev")
	}
	if _, err := ParseMode("everything"); err == nil {
		t.Error("unknown mode accepted")
	}
	names := func(m Mode) map[string]bool {
		out := map[string]bool{}
		for _, s := range Scanners() {
			if s.in(m) {
				out[s.Name] = true
			}
		}
		return out
	}
	dev, media, all := names(ModeDev), names(ModeMedia), names(ModeAll)
	if dev["Big videos"] || !media["Big videos"] || media["Project build folders"] || !dev["Apps"] || !media["Apps"] {
		t.Errorf("wrong scanners per mode: dev=%v media=%v", dev, media)
	}
	if len(all) != len(Scanners()) {
		t.Error("all mode should run every scanner")
	}
	if ModeMedia.NeedsProjects() || !ModeDev.NeedsProjects() {
		t.Error("only media mode skips project discovery")
	}
}

func TestRecordingsAndArchives(t *testing.T) {
	home := t.TempDir()
	desk := filepath.Join(home, "Desktop")
	mkfile(t, filepath.Join(desk, "Screenshot 2026-08-06 at 1.37.59 PM.png"), 2<<20, 60*day)
	mkfile(t, filepath.Join(desk, "Screenshot 2026-11-20 at 9.00.00 AM.png"), 2<<20, 2*day) // too new
	mkfile(t, filepath.Join(desk, "Screen Recording 2026-07-01 at 10.00.00.mov"), 50<<20, 90*day)
	mkfile(t, filepath.Join(desk, "IMG_0004.MOV"), 30<<20, 60*day)
	mkfile(t, filepath.Join(desk, "taxes.pdf"), 1<<20, 300*day)
	mkfile(t, filepath.Join(home, "Documents/Zoom/2026-05-01 Standup/video.mp4"), 10<<20, 100*day)
	mkfile(t, filepath.Join(desk, "old-export.zip"), 200<<20, 90*day)
	mkfile(t, filepath.Join(home, "Documents/Photos Library.photoslibrary/originals/big.zip"), 200<<20, 90*day)
	e := NewEnvMode(home, 0, ModeMedia, nil)
	got := map[string]Item{}
	for _, it := range append(ScanRecordings(e), ScanArchives(e)...) {
		got[it.Title] = it
	}
	for _, want := range []string{"1 old screenshots", "1 screen recordings", "1 phone photos & videos on Desktop", "1 Zoom meeting recordings", "old-export.zip"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q in %v", want, keys(got))
		}
	}
	for _, it := range got {
		if it.Recommended {
			t.Errorf("%s is the user's own content and must not be pre-selected", it.Title)
		}
		for _, p := range it.Paths {
			if strings.Contains(p, "taxes.pdf") || strings.Contains(p, ".photoslibrary") || strings.Contains(p, "11.20") {
				t.Errorf("offered %s", p)
			}
		}
	}
}

func TestLeftoversFromDeletedApps(t *testing.T) {
	home := t.TempDir()
	gone := filepath.Join(home, "Library/Application Support/com.example.goneapp")
	mkfile(t, filepath.Join(gone, "data.db"), 4096, 200*day)
	age(t, gone, 200*day)
	fresh := filepath.Join(home, "Library/Application Support/com.example.stillwriting")
	mkfile(t, filepath.Join(fresh, "data.db"), 4096, 0)
	mkfile(t, filepath.Join(home, "Library/Application Support/com.apple.something/x"), 4096, 200*day)
	var found bool
	for _, it := range ScanApps(NewEnvMode(home, 0, ModeMedia, nil)) {
		if it.Category != CatAppLeftovers {
			continue
		}
		switch it.Title {
		case "com.example.goneapp":
			found = true
			if it.Recommended {
				t.Error("leftovers must not be pre-selected")
			}
		default:
			t.Errorf("unexpected leftover %s", it.Title)
		}
	}
	if !found {
		t.Error("leftovers of a deleted app were not found")
	}
}
