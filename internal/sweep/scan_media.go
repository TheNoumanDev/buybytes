package sweep

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var videoExts = map[string]bool{".mkv": true, ".mp4": true, ".mov": true, ".avi": true, ".webm": true, ".m4v": true, ".wmv": true}

// Test builds and installers people download once and forget.
var installerKinds = []struct {
	label string
	exts  []string
	why   string
}{
	{"APK / AAB / IPA test builds", []string{".apk", ".aab", ".ipa", ".xapk"}, "test builds you already installed"},
	{"App installers (.dmg / .pkg)", []string{".dmg", ".pkg", ".mpkg", ".xip"}, "the apps are installed already"},
	{"Disk images & Windows installers", []string{".iso", ".img", ".exe", ".msi"}, "one-time installers"},
}

var archiveExts = map[string]bool{".zip": true, ".rar": true, ".7z": true, ".tar": true, ".gz": true, ".tgz": true, ".xz": true}

const (
	minVideo      = 200 << 20
	minArchive    = 100 << 20
	bigFolder     = 1 << 30
	installerDays = 7
	untouchedDays = 30
)

// ScanDownloads looks at Downloads and Desktop: movies and series, old
// installers and test builds, big archives, and big forgotten folders.
func ScanDownloads(e *Env) []Item {
	var items []Item
	dl := filepath.Join(e.Home, "Downloads")
	var top []os.DirEntry
	if e.Readable(dl) {
		top, _ = os.ReadDir(dl)
	}
	installers := make([][]string, len(installerKinds))
	installerSizes := make([]int64, len(installerKinds))
	recent := make([][]string, len(installerKinds))
	recentSizes := make([]int64, len(installerKinds))

	classifyFile := func(p string, info fs.FileInfo) {
		ext := strings.ToLower(filepath.Ext(p))
		age := DaysSince(info.ModTime(), e.Now)
		last := maxTime(info.ModTime(), atime(p))
		for i, k := range installerKinds {
			for _, x := range k.exts {
				if ext != x {
					continue
				}
				if age > installerDays {
					installers[i] = append(installers[i], p)
					installerSizes[i] += info.Size()
				} else {
					recent[i] = append(recent[i], p)
					recentSizes[i] += info.Size()
				}
				return
			}
		}
		switch {
		case videoExts[ext] && info.Size() >= minVideo:
			items = append(items, Item{
				Category: CatDownloads, Title: "🎬 " + shortName(filepath.Base(p)), Paths: []string{p}, LastUsed: last,
				Detail: "video in " + tildePath(e.Home, filepath.Dir(p)),
			})
		case archiveExts[ext] && info.Size() >= minArchive && age > untouchedDays:
			items = append(items, Item{
				Category: CatDownloads, Title: "📦 " + shortName(filepath.Base(p)), Paths: []string{p}, LastUsed: last,
				Detail: "archive · delete if you already extracted it",
			})
		}
	}

	for _, en := range top {
		p := filepath.Join(dl, en.Name())
		if strings.HasPrefix(en.Name(), ".") {
			continue
		}
		if !en.IsDir() {
			if info, err := en.Info(); err == nil {
				classifyFile(p, info)
			}
			continue
		}
		// A folder of several videos is a season/series: one item.
		videos, files := 0, 0
		_ = filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				files++
				if videoExts[strings.ToLower(filepath.Ext(q))] {
					if fi, err := d.Info(); err == nil && fi.Size() >= minVideo {
						videos++
					}
				}
			}
			return nil
		})
		last := NewestMtime(p)
		if videos >= 2 && videos*2 >= files {
			items = append(items, Item{
				Category: CatDownloads, Title: "📺 " + shortName(en.Name()), Paths: []string{p}, LastUsed: last,
				Detail: fmt.Sprintf("%d videos · a whole series or season", videos),
			})
			continue
		}
		if size := DirSize(p); size >= bigFolder {
			items = append(items, Item{
				Category: CatDownloads, Title: "📁 " + shortName(en.Name()), Paths: []string{p}, Size: size, LastUsed: last,
				Detail: fmt.Sprintf("folder in Downloads · %d files", files),
			})
			continue
		}
		_ = filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if info, err := d.Info(); err == nil {
					classifyFile(q, info)
				}
			}
			return nil
		})
	}
	for i, k := range installerKinds {
		if len(installers[i]) == 0 {
			continue
		}
		sort.Strings(installers[i])
		items = append(items, Item{
			Category: CatDownloads, Title: fmt.Sprintf("%d %s", len(installers[i]), k.label), Paths: installers[i],
			Size: installerSizes[i], Recommended: true,
			Detail: fmt.Sprintf("in Downloads, older than %d days · %s", installerDays, k.why),
		})
	}
	for i, k := range installerKinds {
		if len(recent[i]) == 0 {
			continue
		}
		sort.Strings(recent[i])
		items = append(items, Item{
			Category: CatDownloads, Title: fmt.Sprintf("%d recent %s", len(recent[i]), k.label), Paths: recent[i],
			Size:   recentSizes[i],
			Detail: fmt.Sprintf("downloaded this week · remove once you're done testing"),
		})
	}

	// Other old videos on the Desktop (screen captures and AirDropped IMG_
	// files are grouped by ScanRecordings).
	desk := filepath.Join(e.Home, "Desktop")
	if !e.Readable(desk) {
		return items
	}
	if entries, err := os.ReadDir(desk); err == nil {
		var recs []string
		var size int64
		for _, en := range entries {
			n := en.Name()
			if en.IsDir() || !videoExts[strings.ToLower(filepath.Ext(n))] || isScreenCapture(n) || strings.HasPrefix(n, "IMG_") {
				continue
			}
			info, err := en.Info()
			if err != nil || DaysSince(info.ModTime(), e.Now) <= untouchedDays {
				continue
			}
			recs = append(recs, filepath.Join(desk, n))
			size += info.Size()
		}
		if len(recs) > 0 {
			items = append(items, Item{
				Category: CatDownloads, Title: fmt.Sprintf("%d old videos on Desktop", len(recs)), Paths: recs, Size: size,
				Detail: "videos older than 30 days",
			})
		}
	}
	return items
}

// shortName trims scene-release noise so "Carrie.S01.1080p.WEB-DL.Hindi…" reads "Carrie S01".
func shortName(n string) string {
	base := strings.TrimSuffix(n, filepath.Ext(n))
	if filepath.Ext(n) == "" || len(filepath.Ext(n)) > 6 {
		base = n
	}
	fields := strings.FieldsFunc(base, func(r rune) bool { return r == '.' || r == '_' })
	var keep []string
	for _, f := range fields {
		lf := strings.ToLower(f)
		if strings.HasSuffix(lf, "p") && len(lf) <= 5 && strings.Trim(lf[:len(lf)-1], "0123456789") == "" && len(lf) > 3 {
			break // resolution tag: the rest is release noise
		}
		keep = append(keep, f)
	}
	out := strings.Join(keep, " ")
	if out == "" {
		out = base
	}
	if len(out) > 48 {
		out = out[:47] + "…"
	}
	return out
}
