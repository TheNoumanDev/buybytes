package sweep

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const staleDeviceDays = 60

func readINI(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func androidSDK(home string) string {
	for _, env := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if v := os.Getenv(env); v != "" && IsDir(v) {
			return v
		}
	}
	return filepath.Join(home, "Library/Android/sdk")
}

// ScanAndroid finds emulators, system images no emulator uses, and old NDKs.
func ScanAndroid(e *Env) []Item {
	var items []Item
	avdRoot := filepath.Join(e.Home, ".android/avd")
	if v := os.Getenv("ANDROID_AVD_HOME"); v != "" {
		avdRoot = v
	}
	usedImages := map[string]bool{}
	// Lock files linger after a crash, so ask the process list who is really running.
	running := string(Run(5*time.Second, "pgrep", "-fl", "qemu-system"))
	inis, _ := filepath.Glob(filepath.Join(avdRoot, "*.ini"))
	for _, ini := range inis {
		meta := readINI(ini)
		dir := meta["path"]
		if dir == "" || !IsDir(dir) {
			dir = strings.TrimSuffix(ini, ".ini") + ".avd"
		}
		if !IsDir(dir) {
			continue
		}
		cfg := readINI(filepath.Join(dir, "config.ini"))
		if s := cfg["image.sysdir.1"]; s != "" {
			usedImages[strings.TrimSuffix(s, "/")] = true
		}
		name := cfg["avd.ini.displayname"]
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(ini), ".ini")
		}
		var last time.Time
		for _, f := range []string{"hardware-qemu.ini", "userdata-qemu.img", "snapshots", "emulator-user.ini"} {
			if fi, err := os.Stat(filepath.Join(dir, f)); err == nil && fi.ModTime().After(last) {
				last = fi.ModTime()
			}
		}
		avdName := strings.TrimSuffix(filepath.Base(ini), ".ini")
		isRunning := strings.Contains(running+" ", "-avd "+avdName+" ") || strings.Contains(running+"\n", "@"+avdName+"\n")
		it := Item{
			Category: CatDevices, Title: "Android emulator: " + name, Paths: []string{dir, ini}, LastUsed: last,
			Detail: strings.TrimSuffix(cfg["image.sysdir.1"], "/"),
		}
		switch {
		case isRunning:
			it.Detail = "running right now · close it first"
			it.Verdict = "close it first"
			it.ReadOnly = true
		case DaysSince(last, e.Now) > staleDeviceDays:
			it.Recommended = true
			it.Detail += " · recreate in Android Studio if needed"
		}
		items = append(items, it)
	}

	sdk := androidSDK(e.Home)
	imgs, _ := filepath.Glob(filepath.Join(sdk, "system-images", "*", "*", "*"))
	var unused []string
	var names []string
	for _, img := range imgs {
		rel, _ := filepath.Rel(sdk, img)
		if !IsDir(img) || usedImages[rel] {
			continue
		}
		unused = append(unused, img)
		parts := strings.Split(rel, string(os.PathSeparator))
		names = append(names, parts[1]+" "+parts[2])
	}
	if len(unused) > 0 {
		items = append(items, Item{
			Category: CatDevices, Title: "Android system images no emulator uses", Paths: unused,
			Regenerable: true, Recommended: true,
			Detail: strings.Join(names, ", ") + " · re-download from SDK Manager",
		})
	}

	if ndks, err := os.ReadDir(filepath.Join(sdk, "ndk")); err == nil && len(ndks) > 1 {
		var vs []string
		for _, n := range ndks {
			if n.IsDir() {
				vs = append(vs, n.Name())
			}
		}
		sort.Slice(vs, func(i, j int) bool { return versionLess(vs[j], vs[i]) })
		var old []string
		for _, v := range vs[1:] {
			old = append(old, filepath.Join(sdk, "ndk", v))
		}
		if len(old) > 0 {
			items = append(items, Item{
				Category: CatToolchain, Title: "Older Android NDKs", Paths: old, Regenerable: true,
				Detail: fmt.Sprintf("keeping %s · check no project pins ndkVersion", vs[0]),
			})
		}
	}
	return items
}

type simRuntime struct {
	Identifier         string `json:"identifier"`
	Version            string `json:"version"`
	PlatformIdentifier string `json:"platformIdentifier"`
	RuntimeIdentifier  string `json:"runtimeIdentifier"`
	SizeBytes          int64  `json:"sizeBytes"`
	LastUsedAt         string `json:"lastUsedAt"`
	Deletable          bool   `json:"deletable"`
}

type simDevice struct {
	UDID         string `json:"udid"`
	Name         string `json:"name"`
	State        string `json:"state"`
	IsAvailable  bool   `json:"isAvailable"`
	DataPath     string `json:"dataPath"`
	DataPathSize int64  `json:"dataPathSize"`
	LastUsedAt   string `json:"lastUsedAt"`
}

var platformName = map[string]string{
	"com.apple.platform.iphonesimulator":  "iOS",
	"com.apple.platform.appletvsimulator": "tvOS",
	"com.apple.platform.watchsimulator":   "watchOS",
	"com.apple.platform.xrsimulator":      "visionOS",
}

// ScanSimulators finds old simulator runtimes, broken simulators and long-unused ones.
func ScanSimulators(e *Env) []Item {
	var items []Item
	if out := Run(60*time.Second, "xcrun", "simctl", "runtime", "list", "-j"); len(out) > 0 {
		var rts map[string]simRuntime
		if json.Unmarshal(out, &rts) == nil {
			byPlat := map[string][]simRuntime{}
			for _, r := range rts {
				byPlat[r.PlatformIdentifier] = append(byPlat[r.PlatformIdentifier], r)
			}
			for plat, list := range byPlat {
				sort.Slice(list, func(i, j int) bool { return versionLess(list[j].Version, list[i].Version) })
				pname := platformName[plat]
				if pname == "" {
					pname = plat
				}
				for i, r := range list {
					last, _ := time.Parse(time.RFC3339, r.LastUsedAt)
					it := Item{
						ID:       NewID(CatDevices, "runtime:"+r.Identifier),
						Category: CatDevices, Title: fmt.Sprintf("%s %s simulator runtime", pname, r.Version),
						Size: r.SizeBytes, LastUsed: last, Regenerable: true,
						Command: []string{"xcrun", "simctl", "runtime", "delete", r.Identifier},
					}
					if !r.Deletable {
						it.ReadOnly = true
						it.Verdict = "Xcode owns it"
						it.Detail = "managed by Xcode, can't be deleted here"
					} else if i == 0 {
						it.Detail = "newest " + pname + " runtime"
						it.Verdict = VerdictKeep
						if DaysSince(last, e.Now) > 30 {
							it.Verdict = VerdictCheck // the only one you have, just not used lately
						}
					} else {
						it.Detail = fmt.Sprintf("you also have %s %s · re-download in Xcode › Settings › Components",
							pname, list[0].Version)
						it.Recommended = DaysSince(last, e.Now) > 14
					}
					items = append(items, it)
				}
			}
		}
	}
	if out := Run(60*time.Second, "xcrun", "simctl", "list", "devices", "-j"); len(out) > 0 {
		var res struct {
			Devices map[string][]simDevice `json:"devices"`
		}
		if json.Unmarshal(out, &res) == nil {
			var brokenSize int64
			var broken int
			for runtime, devs := range res.Devices {
				for _, d := range devs {
					if !d.IsAvailable {
						broken++
						brokenSize += d.DataPathSize
						continue
					}
					last, _ := time.Parse(time.RFC3339, d.LastUsedAt)
					if last.IsZero() {
						last = NewestMtime(d.DataPath)
					}
					if d.State == "Booted" || DaysSince(last, e.Now) <= staleDeviceDays {
						continue
					}
					rt := strings.TrimPrefix(runtime, "com.apple.CoreSimulator.SimRuntime.")
					items = append(items, Item{
						ID:       NewID(CatDevices, "sim:"+d.UDID),
						Category: CatDevices, Title: fmt.Sprintf("Simulator: %s (%s)", d.Name, rt),
						Size: d.DataPathSize, LastUsed: last,
						Detail:  "apps and data inside it",
						Command: []string{"xcrun", "simctl", "delete", d.UDID},
					})
				}
			}
			if broken > 0 {
				items = append(items, Item{
					ID:       NewID(CatDevices, "sim:unavailable"),
					Category: CatDevices, Title: fmt.Sprintf("%d broken simulators", broken), Size: brokenSize,
					Recommended: true, Regenerable: true,
					Detail:  "their runtime is gone, they can't boot anymore",
					Command: []string{"xcrun", "simctl", "delete", "unavailable"},
				})
			}
		}
	}
	return items
}

// versionLess compares dotted versions numerically ("3.9" < "3.10").
func versionLess(a, b string) bool {
	pa, pb := strings.FieldsFunc(a, isSep), strings.FieldsFunc(b, isSep)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		var x, y int
		_, ex := fmt.Sscanf(pa[i], "%d", &x)
		_, ey := fmt.Sscanf(pb[i], "%d", &y)
		if ex != nil || ey != nil {
			if pa[i] != pb[i] {
				return pa[i] < pb[i]
			}
			continue
		}
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

func isSep(r rune) bool { return r == '.' || r == '-' || r == '_' || r == ' ' }
