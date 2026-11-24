// Package demo holds a made-up machine for README screenshots, so real
// project names and paths never end up in images.
package demo

import (
	"time"

	"github.com/TheNoumanDev/buybytes/internal/sweep"
)

const (
	mb = int64(1) << 20
	gb = int64(1) << 30
)

func ago(d int) time.Time { return time.Now().Add(-time.Duration(d) * 24 * time.Hour) }

// Items is a believable scan of a fictional developer's Mac.
func Items() []sweep.Item {
	items := []sweep.Item{
		// Recommended
		{Category: sweep.CatAgents, Title: "~/code/acme-api-claude-fix-auth", Size: 1843 * mb, LastUsed: ago(12), Recommended: true, Detail: "claude/fix-auth is merged into main · clean"},
		{Category: sweep.CatAgents, Title: "~/code/acme-api-codex-refactor", Size: 1210 * mb, LastUsed: ago(41), Recommended: true, Detail: "codex/refactor · clean · untouched, branch is kept"},
		{Category: sweep.CatDev, Title: "Xcode DerivedData", Size: 9420 * mb, Recommended: true, Detail: "14 build caches · Xcode rebuilds them"},
		{Category: sweep.CatDev, Title: "~/code/hackathon-2025", Size: 6250 * mb, LastUsed: ago(240), Recommended: true, Detail: "target · rebuilds itself"},
		{Category: sweep.CatDev, Title: "~/code/weather-flutter", Size: 3120 * mb, LastUsed: ago(47), Recommended: true, Detail: ".dart_tool, build, ios/Pods · rebuilds itself"},
		{Category: sweep.CatDev, Title: "~/code/todo-app-v7", Size: 2410 * mb, LastUsed: ago(95), Recommended: true, Detail: "node_modules · rebuilds itself"},
		{Category: sweep.CatToolchain, Title: "Flutter 3.24.5 (fvm)", Size: 3110 * mb, LastUsed: ago(3), InUse: true, Detail: "in use by weather-flutter, fvm global default"},
		{Category: sweep.CatToolchain, Title: "Flutter 3.22.0 (fvm)", Size: 2940 * mb, LastUsed: ago(150), Recommended: true, Detail: "no project pins it · `fvm install 3.22.0` brings it back"},
		{Category: sweep.CatToolchain, Title: "Flutter 3.19.6 (fvm)", Size: 2610 * mb, LastUsed: ago(210), Recommended: true, Detail: "no project pins it · `fvm install 3.19.6` brings it back"},
		{Category: sweep.CatDevices, Title: "iOS 17.5 simulator runtime", Size: 7930 * mb, LastUsed: ago(120), Recommended: true, Detail: "you also have iOS 18.2 · re-download in Xcode › Settings › Components"},
		{Category: sweep.CatDevices, Title: "6 broken simulators", Size: 2120 * mb, Recommended: true, Detail: "their runtime is gone · `simctl delete unavailable`"},
		{Category: sweep.CatAI, Title: "ollama llama3.1:8b", Size: 4920 * mb, LastUsed: ago(70), Recommended: true, Detail: "no project mentions it · re-downloadable"},
		{Category: sweep.CatDownloads, Title: "4 APK / AAB / IPA test builds", Size: 960 * mb, LastUsed: ago(33), Recommended: true, Detail: "installers you already tested · older than 7 days"},
		{Category: sweep.CatCaches, Title: "npm cache", Size: 1630 * mb, Recommended: true, Regenerable: true, Detail: "~/.npm/_cacache · re-downloads packages on next install"},
		{Category: sweep.CatCaches, Title: "Homebrew downloads", Size: 840 * mb, Recommended: true, Regenerable: true, Detail: "~/Library/Caches/Homebrew · old bottles and installers"},
		{Category: sweep.CatAIData, Title: "Claude Code temp files", Size: 320 * mb, LastUsed: ago(35), Recommended: true, Detail: "1,204 old shell snapshots, rewind copies, debug logs"},

		// Your call
		{Category: sweep.CatDevices, Title: "Android emulator: Pixel 7 Pro", Size: 12400 * mb, LastUsed: ago(9), Detail: "system-images/android-35/google_apis/arm64-v8a"},
		{Category: sweep.CatAI, Title: "stabilityai/sdxl-turbo", Size: 6940 * mb, LastUsed: ago(20), Detail: "mentioned in your code · re-downloadable", Verdict: sweep.VerdictKeep},
		{Category: sweep.CatDownloads, Title: "Startup Diaries S02", Size: 8720 * mb, LastUsed: ago(130), Regenerable: true, Detail: "8 videos · a whole series or season"},
		{Category: sweep.CatDownloads, Title: "Deadline Drift 2024.mkv", Size: 2310 * mb, LastUsed: ago(60), Regenerable: true, Detail: "video in ~/Downloads"},
		{Category: sweep.CatAIData, Title: "Claude Code chats for 6 deleted folders", Size: 410 * mb, LastUsed: ago(52), Verdict: sweep.VerdictProbably, Detail: "38 chats whose project folder is gone · memory notes are kept"},
		{Category: sweep.CatAIData, Title: "Cursor state for 4 deleted folders", Size: 350 * mb, LastUsed: ago(80), Verdict: sweep.VerdictProbably, Detail: "per-project editor state and AI chats for folders that no longer exist"},
		{Category: sweep.CatCaches, Title: "Dart pub cache", Size: 3300 * mb, Detail: "~/.pub-cache · run `flutter pub get` again in each project after"},
		{Category: sweep.CatDev, Title: "~/code/acme-web", Size: 1480 * mb, LastUsed: ago(0), Regenerable: true, Detail: "node_modules, .next · active project, next build will be slower"},

		// Needs you first
		{Category: sweep.CatDevices, Title: "Android emulator: Pixel Tablet", Size: 9800 * mb, LastUsed: ago(1), ReadOnly: true, Verdict: "close it first", Detail: "running right now · close it first"},
		{Category: sweep.CatAgents, Title: "~/code/acme-web-agent-wip", Size: 910 * mb, LastUsed: ago(1), ReadOnly: true, Verdict: "commit first", Detail: "agent/new-onboarding · has uncommitted changes, commit or stash first"},
		{Category: sweep.CatAIData, Title: "Cursor cache", Size: 1150 * mb, ReadOnly: true, Verdict: "quit it first", Detail: "Cursor is open · quit it, then scan again"},
	}
	for i := range items {
		items[i].ID = sweep.NewID(items[i].Category, items[i].Title)
		if items[i].Verdict == "" {
			items[i].Verdict = sweep.DefaultVerdict(items[i], time.Now())
		}
	}
	sweep.SortItems(items)
	return items
}
