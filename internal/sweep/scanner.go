package sweep

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// DiscoveryShare is the part of the progress bar used by project discovery.
const DiscoveryShare = 0.3

// Scanner produces items. Items may leave Size at 0; the runner sizes Paths.
type Scanner struct {
	Name string
	Run  func(e *Env) []Item
}

// Scanners is the full list, in display order.
func Scanners() []Scanner {
	return []Scanner{
		{"Agent worktrees", ScanWorktrees},
		{"Project build folders", ScanProjectArtifacts},
		{"Xcode", ScanXcode},
		{"Gradle", ScanGradle},
		{"Flutter versions (fvm)", ScanFVM},
		{"Android emulators", ScanAndroid},
		{"iOS simulators", ScanSimulators},
		{"AI models", ScanAIModels},
		{"AI chats & app caches", ScanAIData},
		{"Downloads", ScanDownloads},
		{"Package caches", ScanCaches},
		{"Docker", ScanDocker},
		{"Device backups", ScanBackups},
	}
}

// ScanAll runs every scanner concurrently, sizes items, drops small ones and
// sorts: recommended first, then by category order and size.
func ScanAll(e *Env, progress Progress) []Item {
	if progress == nil {
		progress = func(float64, string) {}
	}
	var mu sync.Mutex
	var all []Item
	var wg sync.WaitGroup
	scanners := Scanners()
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
		progress(DiscoveryShare+(1-DiscoveryShare)*float64(done)/float64(len(scanners)), label)
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
