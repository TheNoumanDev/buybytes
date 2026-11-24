package sweep

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	unusedAppDays = 90 // not opened for this long: "probably remove"
	recentAppDays = 30 // opened within this: not listed as unused
)

// Apps that macOS protects, or that manage the Mac itself (work device
// management, security agents): never offered here.
var keepApps = map[string]bool{
	"com.apple.Safari":                   true,
	"com.microsoft.CompanyPortalMac":     true,
	"com.jamfsoftware.selfservice.mac":   true,
	"com.jamf.connect":                   true,
	"com.kandji.Kandji-Self-Service":     true,
	"com.crowdstrike.falcon.App":         true,
	"com.sentinelone.SentinelOne":        true,
	"com.paloaltonetworks.GlobalProtect": true,
	"com.cisco.anyconnect.gui":           true,
}

// App is an installed .app bundle.
type App struct {
	Name, Path, ID string
	LastUsed       time.Time
}

// installedApps lists apps in /Applications (and one folder down) and ~/Applications.
func installedApps(home string) []App {
	var paths []string
	for _, root := range []string{"/Applications", filepath.Join(home, "Applications")} {
		for _, pat := range []string{"*.app", "*/*.app"} {
			m, _ := filepath.Glob(filepath.Join(root, pat))
			paths = append(paths, m...)
		}
	}
	apps := make([]App, len(paths))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, p := range paths {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			a := App{Name: strings.TrimSuffix(filepath.Base(p), ".app"), Path: p}
			a.ID = strings.TrimSpace(string(Run(5*time.Second, "plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", filepath.Join(p, "Contents", "Info.plist"))))
			if out := strings.TrimSpace(string(Run(5*time.Second, "mdls", "-raw", "-name", "kMDItemLastUsedDate", p))); out != "" && out != "(null)" {
				a.LastUsed, _ = time.Parse("2006-01-02 15:04:05 -0700", out)
			}
			apps[i] = a
		}(i, p)
	}
	wg.Wait()
	return apps
}

// appLeftovers are the files an app leaves in ~/Library. Containers are left
// out on purpose: macOS guards them and asks before anything touches them.
func appLeftovers(home, id, name string) []string {
	lib := filepath.Join(home, "Library")
	var cands []string
	if id != "" {
		cands = append(cands,
			filepath.Join(lib, "Caches", id),
			filepath.Join(lib, "Application Support", id),
			filepath.Join(lib, "Preferences", id+".plist"),
			filepath.Join(lib, "Saved Application State", id+".savedState"),
			filepath.Join(lib, "HTTPStorages", id),
			filepath.Join(lib, "WebKit", id),
		)
	}
	if name != "" {
		cands = append(cands, filepath.Join(lib, "Application Support", name), filepath.Join(lib, "Logs", name))
	}
	var out []string
	for _, c := range cands {
		if Exists(c) {
			out = append(out, c)
		}
	}
	return out
}

func appRunningAt(path string) bool {
	if path == "" {
		return false
	}
	return appRunning(filepath.Join(path, "Contents", "MacOS") + "/")
}

// ScanApps finds apps you haven't opened in a while (with their leftovers),
// caches of apps you still have, and leftovers from apps you deleted.
func ScanApps(e *Env) []Item {
	apps := installedApps(e.Home)
	ids := map[string]string{} // bundle id -> app name
	for _, a := range apps {
		if a.ID != "" {
			ids[a.ID] = a.Name
		}
	}
	var items []Item
	listed := map[string]bool{} // paths already inside an app item

	// 1. Apps not opened for a while.
	for _, a := range apps {
		if keepApps[a.ID] || (!a.LastUsed.IsZero() && DaysSince(a.LastUsed, e.Now) <= recentAppDays) {
			continue
		}
		paths := append([]string{a.Path}, appLeftovers(e.Home, a.ID, a.Name)...)
		for _, p := range paths {
			listed[p] = true
		}
		it := Item{Category: CatApps, Title: a.Name, Paths: paths, LastUsed: a.LastUsed, ID: NewID(CatApps, a.Path)}
		extra := ""
		if n := len(paths) - 1; n > 0 {
			extra = fmt.Sprintf(" · plus its cache and settings (%s)", plural(n, "folder"))
		}
		switch {
		case appRunningAt(a.Path):
			it.ReadOnly, it.Verdict = true, "quit it first"
			it.Detail = "open right now · quit it, then scan again"
		case a.LastUsed.IsZero():
			it.Verdict = VerdictCheck
			it.Detail = "macOS has no record of you opening it" + extra
		case DaysSince(a.LastUsed, e.Now) > unusedAppDays:
			it.Verdict = VerdictProbably
			it.Detail = "not opened in months · reinstall it if you need it" + extra
		default:
			it.Verdict = VerdictCheck
			it.Detail = "not opened this month" + extra
		}
		items = append(items, it)
	}

	// 2. Caches of apps you still use, and 3. leftovers of apps you deleted.
	covered := map[string]bool{}
	for _, c := range cacheSpecs {
		covered[filepath.Join(e.Home, c.rel)] = true
	}
	caches := filepath.Join(e.Home, "Library", "Caches")
	entries, _ := os.ReadDir(caches)
	orphans := map[string][]string{} // bundle id -> leftover paths
	for _, en := range entries {
		name := en.Name()
		p := filepath.Join(caches, name)
		if !en.IsDir() || listed[p] || covered[p] || strings.HasPrefix(name, "com.apple.") || strings.HasSuffix(name, ".ShipIt") {
			continue
		}
		appName, installed := ids[name]
		if !installed && looksLikeBundleID(name) && !installedPrefix(ids, name) {
			orphans[name] = append(orphans[name], p)
			continue
		}
		title := name
		if installed {
			title = appName
		}
		it := Item{Category: CatAppCaches, Title: title + " cache", Paths: []string{p}, Regenerable: true,
			Detail: "~/Library/Caches/" + name + " · rebuilt when the app needs it"}
		if installed && appRunningAt(appPath(apps, name)) {
			it.ReadOnly, it.Verdict = true, "quit it first"
			it.Detail = appName + " is open · quit it, then scan again"
		} else if installed {
			it.Recommended = true
		}
		items = append(items, it)
	}
	for _, dir := range []string{"Application Support", "Saved Application State", "HTTPStorages", "WebKit"} {
		entries, _ := os.ReadDir(filepath.Join(e.Home, "Library", dir))
		for _, en := range entries {
			id := strings.TrimSuffix(en.Name(), ".savedState")
			if !looksLikeBundleID(id) || strings.HasPrefix(id, "com.apple.") || ids[id] != "" || installedPrefix(ids, id) {
				continue
			}
			orphans[id] = append(orphans[id], filepath.Join(e.Home, "Library", dir, en.Name()))
		}
	}
	for id, paths := range orphans {
		last := time.Time{}
		for _, p := range paths {
			if t := NewestMtime(p); t.After(last) {
				last = t
			}
		}
		if DaysSince(last, e.Now) <= unusedAppDays {
			continue // something still writes here; maybe a helper or tool we don't know
		}
		items = append(items, Item{Category: CatAppLeftovers, Title: id, Paths: paths, LastUsed: last,
			Verdict: VerdictCheck,
			Detail:  fmt.Sprintf("app isn't installed anymore · %d leftover folders (cache, settings, data)", len(paths))})
	}
	return items
}

var bundleIDRe = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9_-]+){2,}$`)

func looksLikeBundleID(s string) bool { return bundleIDRe.MatchString(s) }

// installedPrefix: helpers and extensions use the main app's id as a prefix
// (com.spotify.client.helper belongs to com.spotify.client), and the other way round.
func installedPrefix(ids map[string]string, id string) bool {
	for k := range ids {
		if strings.HasPrefix(id, k+".") || strings.HasPrefix(k, id+".") {
			return true
		}
	}
	return false
}

func appPath(apps []App, id string) string {
	for _, a := range apps {
		if a.ID == id {
			return a.Path
		}
	}
	return ""
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
