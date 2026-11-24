package sweep

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	oldCaptureDays = 30 // screenshots and recordings older than this
	minBigVideo    = 500 << 20
	staleVideoDays = 180
	minLooseFile   = 100 << 20
)

var capturePrefixes = []string{"Screen Recording", "Screenshot", "Screen Shot", "CleanShot", "Simulator Screen Recording", "Simulator Screenshot"}

// isScreenCapture matches the names macOS, the iOS Simulator and CleanShot give captures.
func isScreenCapture(name string) bool {
	for _, p := range capturePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// Folders inside media roots that are apps' libraries, never loose files.
func isLibraryBundle(name string) bool {
	for _, s := range []string{".photoslibrary", ".fcpbundle", ".imovielibrary", ".musiclibrary", ".tvlibrary", ".app", ".logicx", ".band"} {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return name == "iMovie Library" || name == "TV" || name == "Music" || name == "Photo Booth Library"
}

var skipWalk = map[string]bool{"node_modules": true, ".git": true, "venv": true, ".venv": true, "Library": true, "build": true, "Pods": true}

// walkFiles calls fn for regular files under root, down to maxDepth folders,
// skipping hidden folders, app libraries and dependency folders.
func walkFiles(root string, maxDepth int, fn func(p string, info fs.FileInfo)) {
	base := strings.Count(root, string(filepath.Separator))
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || skipWalk[name] || isLibraryBundle(name) ||
				strings.Count(p, string(filepath.Separator))-base >= maxDepth) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				fn(p, info)
			}
		}
		return nil
	})
}

// screenshotFolder is where macOS saves screenshots, if the user changed it.
func screenshotFolder(home string) string {
	out := strings.TrimSpace(string(Run(3*time.Second, "defaults", "read", "com.apple.screencapture", "location")))
	if out == "" {
		return ""
	}
	if strings.HasPrefix(out, "~") {
		out = filepath.Join(home, out[1:])
	}
	return out
}

// ScanRecordings groups screen recordings, screenshots, phone media dropped on
// the Desktop, and Zoom meeting recordings.
func ScanRecordings(e *Env) []Item {
	roots := []string{"Desktop", "Documents", "Downloads", "Movies", "Pictures"}
	seenRoot := map[string]bool{}
	var dirs []string
	for _, r := range roots {
		dirs = append(dirs, filepath.Join(e.Home, r))
	}
	if s := screenshotFolder(e.Home); s != "" {
		dirs = append(dirs, s)
	}
	type group struct {
		paths []string
		size  int64
		last  time.Time
	}
	add := func(g *group, p string, info fs.FileInfo) {
		g.paths = append(g.paths, p)
		g.size += info.Size()
		if info.ModTime().After(g.last) {
			g.last = info.ModTime()
		}
	}
	var recs, shots, phone group
	for _, dir := range dirs {
		if seenRoot[dir] || !IsDir(dir) || !e.Readable(dir) {
			continue
		}
		seenRoot[dir] = true
		entries, _ := os.ReadDir(dir) // captures land at the top level
		for _, en := range entries {
			info, err := en.Info()
			if err != nil || !info.Mode().IsRegular() || DaysSince(info.ModTime(), e.Now) <= oldCaptureDays {
				continue
			}
			name, p := en.Name(), filepath.Join(dir, en.Name())
			ext := strings.ToLower(filepath.Ext(name))
			switch {
			case isScreenCapture(name) && videoExts[ext]:
				add(&recs, p, info)
			case isScreenCapture(name):
				add(&shots, p, info)
			case filepath.Base(dir) == "Desktop" && strings.HasPrefix(name, "IMG_"):
				add(&phone, p, info)
			}
		}
	}
	var items []Item
	if len(recs.paths) > 0 {
		items = append(items, Item{Category: CatRecordings, Title: fmt.Sprintf("%d screen recordings", len(recs.paths)),
			Paths: recs.paths, Size: recs.size, LastUsed: recs.last, Verdict: VerdictProbably,
			Detail: fmt.Sprintf("older than %d days · keep the ones you still need first", oldCaptureDays)})
	}
	if len(shots.paths) > 0 {
		items = append(items, Item{Category: CatRecordings, Title: fmt.Sprintf("%d old screenshots", len(shots.paths)),
			Paths: shots.paths, Size: shots.size, LastUsed: shots.last, Verdict: VerdictProbably,
			Detail: fmt.Sprintf("older than %d days, on Desktop and other folders", oldCaptureDays)})
	}
	if len(phone.paths) > 0 {
		items = append(items, Item{Category: CatRecordings, Title: fmt.Sprintf("%d phone photos & videos on Desktop", len(phone.paths)),
			Paths: phone.paths, Size: phone.size, LastUsed: phone.last, Verdict: VerdictCheck,
			Detail: "AirDropped IMG_ files · remove only if they're in Photos already"})
	}
	// Zoom saves each meeting in its own folder.
	zoom := filepath.Join(e.Home, "Documents", "Zoom")
	if entries, err := os.ReadDir(zoom); err == nil && e.Readable(zoom) {
		var paths []string
		var last time.Time
		for _, en := range entries {
			if !en.IsDir() {
				continue
			}
			p := filepath.Join(zoom, en.Name())
			paths = append(paths, p)
			if t := NewestMtime(p); t.After(last) {
				last = t
			}
		}
		if len(paths) > 0 {
			items = append(items, Item{Category: CatRecordings, Title: fmt.Sprintf("%d Zoom meeting recordings", len(paths)),
				Paths: paths, LastUsed: last, Verdict: VerdictCheck,
				Detail: "~/Documents/Zoom · download them from the cloud first if you need them"})
		}
	}
	return items
}

// ScanVideos lists big video files in Movies and Documents (Downloads and the
// Desktop are covered by the Downloads scanner).
func ScanVideos(e *Env) []Item {
	var items []Item
	for _, r := range []string{"Movies", "Documents"} {
		root := filepath.Join(e.Home, r)
		if !e.Readable(root) {
			continue
		}
		walkFiles(root, 5, func(p string, info fs.FileInfo) {
			name := info.Name()
			if !videoExts[strings.ToLower(filepath.Ext(name))] || info.Size() < minBigVideo || isScreenCapture(name) {
				return
			}
			last := maxTime(atime(p), info.ModTime())
			// "source.mp4" says nothing; "gta-launch/source.mp4" does.
			title := filepath.Base(filepath.Dir(p)) + "/" + name
			it := Item{Category: CatVideos, Title: title, Paths: []string{p}, Size: info.Size(), LastUsed: last,
				Detail: "video in " + tildePath(e.Home, filepath.Dir(p))}
			if DaysSince(last, e.Now) > staleVideoDays {
				it.Verdict = VerdictProbably
			}
			items = append(items, it)
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Size > items[j].Size })
	return items
}

// ScanArchives lists zips, disk images and installers left outside Downloads.
func ScanArchives(e *Env) []Item {
	installer := map[string]bool{}
	for _, k := range installerKinds {
		for _, x := range k.exts {
			installer[x] = true
		}
	}
	var items []Item
	for _, r := range []string{"Desktop", "Documents", "Movies"} {
		root := filepath.Join(e.Home, r)
		if !e.Readable(root) {
			continue
		}
		walkFiles(root, 3, func(p string, info fs.FileInfo) {
			ext := strings.ToLower(filepath.Ext(info.Name()))
			if !(archiveExts[ext] || installer[ext]) || info.Size() < minLooseFile || DaysSince(info.ModTime(), e.Now) <= untouchedDays {
				return
			}
			kind := "archive · delete if you already unpacked it"
			if installer[ext] {
				kind = "installer · the app is probably installed already"
			}
			items = append(items, Item{Category: CatArchives, Title: info.Name(), Paths: []string{p}, Size: info.Size(),
				LastUsed: info.ModTime(), Verdict: VerdictProbably,
				Detail: kind + " · in " + tildePath(e.Home, filepath.Dir(p))})
		})
	}
	return items
}
