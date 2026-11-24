package sweep

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// activeDays: projects touched more recently than this keep their build
// folders unless the user picks them (deleting them just slows the next build).
const activeDays = 7

// artifactDirs maps a project marker to the regenerable folders it produces.
var artifactDirs = map[string][]string{
	"pubspec.yaml":        {"build", ".dart_tool", "ios/Pods", "macos/Pods", "android/.gradle"},
	"package.json":        {"node_modules", ".next", ".turbo", ".parcel-cache", ".nuxt", ".svelte-kit", ".expo"},
	"Cargo.toml":          {"target"},
	"build.gradle":        {"build", ".gradle", "app/build"},
	"build.gradle.kts":    {"build", ".gradle", "app/build"},
	"settings.gradle":     {"build", ".gradle", "app/build"},
	"settings.gradle.kts": {"build", ".gradle", "app/build"},
	"Podfile":             {"Pods"},
	"Package.swift":       {".build"},
}

// ScanProjectArtifacts finds build output and dependency folders per project.
func ScanProjectArtifacts(e *Env) []Item {
	var items []Item
	seen := map[string]bool{} // a Flutter app's android/ is also a Gradle project: claim each folder once
	for _, p := range e.Projects {
		var paths []string
		var kinds []string
		for m := range p.Markers {
			for _, d := range artifactDirs[m] {
				full := filepath.Join(p.Path, d)
				if seen[full] || !IsDir(full) {
					continue
				}
				seen[full] = true
				paths = append(paths, full)
				kinds = append(kinds, d)
			}
		}
		if len(paths) == 0 {
			continue
		}
		sort.Strings(kinds)
		days := DaysSince(p.LastActive, e.Now)
		it := Item{
			Category:    CatDev,
			Title:       tildePath(e.Home, p.Path),
			Paths:       paths,
			LastUsed:    p.LastActive,
			Regenerable: true,
			Recommended: days > activeDays,
		}
		if it.Recommended {
			it.Detail = fmt.Sprintf("%s · rebuilds itself", strings.Join(kinds, ", "))
		} else {
			it.Detail = fmt.Sprintf("%s · active project, next build will be slower", strings.Join(kinds, ", "))
		}
		items = append(items, it)
	}
	// Python virtualenvs: regenerable but need a reinstall, so always the user's call.
	for _, p := range e.Projects {
		if !p.Markers["pyproject.toml"] && !p.Markers["requirements.txt"] {
			continue
		}
		for _, d := range []string{".venv", "venv"} {
			full := filepath.Join(p.Path, d)
			if IsDir(full) {
				items = append(items, Item{
					Category: CatDev, Title: tildePath(e.Home, full), Paths: []string{full}, LastUsed: p.LastActive,
					Regenerable: true,
					Detail:      "Python venv · recreate with pip/uv install",
				})
			}
		}
	}
	return items
}

// ScanXcode covers DerivedData, old device support files and archives.
func ScanXcode(e *Env) []Item {
	dev := filepath.Join(e.Home, "Library/Developer/Xcode")
	var items []Item
	if dd := filepath.Join(dev, "DerivedData"); IsDir(dd) {
		if entries, _ := os.ReadDir(dd); len(entries) > 0 {
			var paths []string
			for _, en := range entries {
				paths = append(paths, filepath.Join(dd, en.Name()))
			}
			items = append(items, Item{
				Category: CatDev, Title: "Xcode DerivedData", Paths: paths, Regenerable: true, Recommended: true,
				Detail: fmt.Sprintf("%d build caches · Xcode rebuilds them", len(paths)),
			})
		}
	}
	// Device support: one folder per iOS/watchOS/tvOS version a device was plugged in with.
	// Keep the newest per platform.
	for _, platform := range []string{"iOS DeviceSupport", "watchOS DeviceSupport", "tvOS DeviceSupport", "visionOS DeviceSupport"} {
		dir := filepath.Join(dev, platform)
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) < 2 {
			continue
		}
		type ver struct {
			path string
			mod  time.Time
		}
		var vs []ver
		for _, en := range entries {
			if en.IsDir() {
				fi, _ := en.Info()
				vs = append(vs, ver{filepath.Join(dir, en.Name()), fi.ModTime()})
			}
		}
		sort.Slice(vs, func(i, j int) bool { return vs[i].mod.After(vs[j].mod) })
		var old []string
		for _, v := range vs[1:] {
			old = append(old, v.path)
		}
		if len(old) > 0 {
			items = append(items, Item{
				Category: CatToolchain, Title: "Old " + platform, Paths: old, Regenerable: true, Recommended: true,
				Detail: fmt.Sprintf("%d old versions · Xcode re-copies them when that device connects", len(old)),
			})
		}
	}
	if ar := filepath.Join(dev, "Archives"); IsDir(ar) {
		items = append(items, Item{
			Category: CatToolchain, Title: "Xcode Archives", Paths: []string{ar},
			Detail: "your shipped builds and dSYMs · keep if you need crash symbolication",
		})
	}
	return items
}

var gradleVerDir = regexp.MustCompile(`^\d+\.\d+(\.\d+)?(-[\w.]+)?$`)
var wrapperURL = regexp.MustCompile(`gradle-([\d.]+(?:-[\w.]+)?)-(?:bin|all)\.zip`)

// ScanGradle finds caches and wrapper distributions for Gradle versions no project uses.
func ScanGradle(e *Env) []Item {
	used := map[string]bool{}
	for _, p := range e.Projects {
		for _, rel := range []string{"gradle/wrapper/gradle-wrapper.properties", "android/gradle/wrapper/gradle-wrapper.properties"} {
			b, err := os.ReadFile(filepath.Join(p.Path, rel))
			if err != nil {
				continue
			}
			if m := wrapperURL.FindSubmatch(b); m != nil {
				used[string(m[1])] = true
			}
		}
	}
	var items []Item
	g := filepath.Join(e.Home, ".gradle")
	var oldCaches []string
	var oldNames []string
	if entries, err := os.ReadDir(filepath.Join(g, "caches")); err == nil {
		for _, en := range entries {
			if en.IsDir() && gradleVerDir.MatchString(en.Name()) && !used[en.Name()] {
				oldCaches = append(oldCaches, filepath.Join(g, "caches", en.Name()))
				oldNames = append(oldNames, en.Name())
			}
		}
	}
	if dists, err := os.ReadDir(filepath.Join(g, "wrapper/dists")); err == nil {
		for _, en := range dists {
			v := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(en.Name(), "gradle-"), "-bin"), "-all")
			if !used[v] {
				oldCaches = append(oldCaches, filepath.Join(g, "wrapper/dists", en.Name()))
				oldNames = append(oldNames, "wrapper "+v)
			}
		}
	}
	if len(oldCaches) > 0 {
		items = append(items, Item{
			Category: CatToolchain, Title: "Gradle versions no project uses", Paths: oldCaches,
			Regenerable: true, Recommended: true,
			Detail: strings.Join(oldNames, ", ") + " · re-downloads if a project needs it",
		})
	}
	if mods := filepath.Join(g, "caches/modules-2"); IsDir(mods) {
		items = append(items, Item{
			Category: CatCaches, Title: "Gradle dependency cache", Paths: []string{mods}, Regenerable: true,
			Detail: "all downloaded Android/Java libraries · first build after re-downloads them",
		})
	}
	return items
}

// ScanFVM finds Flutter SDK versions installed with fvm that no project pins.
func ScanFVM(e *Env) []Item {
	users := map[string][]string{} // version -> who uses it
	use := func(v, who string) {
		for _, u := range users[v] {
			if u == who {
				return
			}
		}
		users[v] = append(users[v], who)
	}
	for _, p := range e.Projects {
		name := filepath.Base(p.Path)
		if b, err := os.ReadFile(filepath.Join(p.Path, ".fvmrc")); err == nil {
			var v struct {
				Flutter string `json:"flutter"`
			}
			if json.Unmarshal(b, &v) == nil && v.Flutter != "" {
				use(v.Flutter, name)
			}
		}
		if b, err := os.ReadFile(filepath.Join(p.Path, ".fvm/fvm_config.json")); err == nil {
			var v struct {
				V string `json:"flutterSdkVersion"`
			}
			if json.Unmarshal(b, &v) == nil && v.V != "" {
				use(v.V, name)
			}
		}
		// .fvm/flutter_sdk symlink points at the version folder.
		if t, err := os.Readlink(filepath.Join(p.Path, ".fvm/flutter_sdk")); err == nil {
			use(filepath.Base(t), name)
		}
	}
	var items []Item
	for _, root := range []string{filepath.Join(e.Home, "fvm"), filepath.Join(e.Home, ".fvm")} {
		if t, err := os.Readlink(filepath.Join(root, "default")); err == nil {
			use(filepath.Base(t), "fvm global default")
		}
		entries, err := os.ReadDir(filepath.Join(root, "versions"))
		if err != nil {
			continue
		}
		for _, en := range entries {
			if !en.IsDir() {
				continue
			}
			v := en.Name()
			full := filepath.Join(root, "versions", v)
			it := Item{
				Category: CatToolchain, Title: "Flutter " + v + " (fvm)", Paths: []string{full}, Regenerable: true,
				LastUsed: NewestMtime(full),
			}
			if who := users[v]; len(who) > 0 {
				// Listed unchecked so the user sees which versions stay.
				sort.Strings(who)
				names := strings.Join(who[:min(2, len(who))], ", ")
				if len(who) > 2 {
					names += fmt.Sprintf(" +%d more", len(who)-2)
				}
				it.InUse = true
				it.Detail = "in use by " + names
			} else {
				it.Recommended = true
				it.Detail = "no project pins it · `fvm install " + v + "` brings it back"
			}
			items = append(items, it)
		}
	}
	return items
}

func tildePath(home, p string) string {
	if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return "~/" + rel
	}
	return p
}
