package sweep

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// DiscoveryShare is the part of the progress bar used by project discovery.
const DiscoveryShare = 0.3

// Mode picks which scanners run.
type Mode string

const (
	ModeDev   Mode = "dev"   // developer leftovers (the default)
	ModeMedia Mode = "media" // recordings, videos, archives, downloads, apps
	ModeAll   Mode = "all"   // everything
)

// ParseMode reads a --mode value.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(s))); m {
	case ModeDev, ModeMedia, ModeAll:
		return m, nil
	case "":
		return ModeDev, nil
	}
	return "", fmt.Errorf("unknown mode %q (use dev, media or all)", s)
}

// NeedsProjects reports whether the mode's scanners use project discovery.
func (m Mode) NeedsProjects() bool { return m != ModeMedia }

// Scanner produces items. Items may leave Size at 0; the runner sizes Paths.
type Scanner struct {
	Name  string
	Run   func(e *Env) []Item
	Modes []Mode // modes besides "all" that include it
}

func (s Scanner) in(m Mode) bool {
	if m == ModeAll {
		return true
	}
	for _, x := range s.Modes {
		if x == m {
			return true
		}
	}
	return false
}

var (
	dev   = []Mode{ModeDev}
	media = []Mode{ModeMedia}
	both  = []Mode{ModeDev, ModeMedia}
)

// Scanners is the full list, in display order.
func Scanners() []Scanner {
	return []Scanner{
		{"Agent worktrees", ScanWorktrees, dev},
		{"Project build folders", ScanProjectArtifacts, dev},
		{"Xcode", ScanXcode, dev},
		{"Gradle", ScanGradle, dev},
		{"Flutter versions (fvm)", ScanFVM, dev},
		{"Android emulators", ScanAndroid, dev},
		{"iOS simulators", ScanSimulators, dev},
		{"AI models", ScanAIModels, dev},
		{"AI chats & app caches", ScanAIData, dev},
		{"Apps", ScanApps, both},
		{"Downloads", ScanDownloads, both},
		{"Screen & Zoom recordings", ScanRecordings, both},
		{"Big videos", ScanVideos, media},
		{"Archives & installers", ScanArchives, media},
		{"Package caches", ScanCaches, dev},
		{"Docker", ScanDocker, dev},
		{"Device backups", ScanBackups, both},
	}
}

// ScanAll runs the developer-mode scanners.
func ScanAll(e *Env, progress Progress) []Item { return Scan(e, ModeDev, progress) }

// Scan runs the mode's scanners concurrently, sizes items, drops small ones
// and sorts them for display.
func Scan(e *Env, mode Mode, progress Progress) []Item {
	if progress == nil {
		progress = func(float64, string) {}
	}
	var mu sync.Mutex
	var all []Item
	var wg sync.WaitGroup
	var scanners []Scanner
	for _, s := range Scanners() {
		if s.in(mode) {
			scanners = append(scanners, s)
		}
	}
	share := DiscoveryShare
	if e.skipDiscovery {
		share = 0
	}
	pending := map[string]bool{}
	for _, s := range scanners {
		pending[s.Name] = true
	}
	report := func() { // call with mu held
		var left []string
		for _, s := range scanners {
			if pending[s.Name] {
				left = append(left, s.Name)
			}
		}
		label := "done"
		if len(left) > 0 {
			label = "checking " + strings.Join(left[:min(3, len(left))], ", ")
			if len(left) > 3 {
				label += fmt.Sprintf(" +%d more", len(left)-3)
			}
		}
		done := len(scanners) - len(left)
		progress(share+(1-share)*float64(done)/float64(len(scanners)), label)
	}
	mu.Lock()
	report()
	mu.Unlock()
	for _, s := range scanners {
		wg.Add(1)
		go func(s Scanner) {
			defer wg.Done()
			items := s.Run(e)
			for i := range items {
				if items[i].Size == 0 && len(items[i].Paths) > 0 {
					items[i].Size = SizeAll(items[i].Paths)
				}
				if items[i].Verdict == "" {
					items[i].Verdict = DefaultVerdict(items[i], e.Now)
				}
				if items[i].ID == "" {
					key := items[i].Title
					if len(items[i].Paths) > 0 {
						key = items[i].Paths[0]
					}
					items[i].ID = NewID(items[i].Category, key)
				}
			}
			mu.Lock()
			all = append(all, items...)
			delete(pending, s.Name)
			report()
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	kept := all[:0]
	for _, it := range all {
		if it.Size >= e.MinSize {
			kept = append(kept, it)
		}
	}
	SortItems(kept)
	return kept
}

// SortItems orders items for display.
func SortItems(items []Item) {
	rank := map[Category]int{}
	for i, c := range CategoryOrder {
		rank[c] = i
	}
	// Section: 0 recommended (and in-use siblings), 1 your call, 2 read-only.
	section := func(it Item) int {
		switch {
		case it.ReadOnly:
			return 2
		case it.Recommended || it.InUse:
			return 0
		}
		return 1
	}
	// Biggest categories first inside each section, biggest items first inside each category.
	type key struct {
		sec int
		cat Category
	}
	totals := map[key]int64{}
	for _, it := range items {
		totals[key{section(it), it.Category}] += it.Size
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		sa, sb := section(a), section(b)
		if sa != sb {
			return sa < sb
		}
		if a.Category != b.Category {
			ta, tb := totals[key{sa, a.Category}], totals[key{sb, b.Category}]
			if ta != tb {
				return ta > tb
			}
			return rank[a.Category] < rank[b.Category]
		}
		return a.Size > b.Size
	})
}
