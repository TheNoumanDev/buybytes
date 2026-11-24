package tui

import (
	"strings"
	"time"

	"github.com/TheNoumanDev/buybytes/internal/sweep"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// DemoScreen renders one screen of the UI from made-up items, in full colour,
// for README screenshots. name is scanning, list, confirm or done.
func DemoScreen(name string, items []sweep.Item, w, h int) string {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	sp := spinner.New()
	sp.Spinner = spinner.Line // the screenshot font has no braille dots
	sp.Style = lipgloss.NewStyle().Foreground(accent)
	m := &model{spin: sp, selected: map[int]bool{}, expanded: map[int]bool{}, width: w, height: h,
		now: time.Now(), freeBefore: 38 << 30}
	switch name {
	case "scanning":
		m.frac, m.label = 0.64, "checking AI models, Downloads, Package caches +2 more"
		return m.View()
	}
	m.Update(scannedMsg{items: items, projects: 1204, took: 14 * time.Second})
	if name == "list" {
		m.move(2) // highlight an item row, not the category
		m.clampScroll()
		return m.View()
	}
	if name == "confirm" {
		m.phase = phaseConfirm
		return m.View()
	}
	// done: everything recommended went to the Trash.
	// Simulators and models are removed by their own tools and freed at once;
	// the rest goes to the Trash.
	var removed []sweep.Item
	var freedNow int64
	for i, it := range m.items {
		if !m.selected[i] {
			continue
		}
		r := sweep.Result{Item: it}
		if it.Category == sweep.CatDevices || it.Category == sweep.CatAI {
			freedNow += it.Size
		} else {
			r.Moved = []string{"/Users/you/.Trash/x"}
		}
		m.results = append(m.results, r)
		removed = append(removed, it)
	}
	m.freeAfter = m.freeBefore + freedNow
	// The screenshot font has no emoji.
	m.post = strings.ReplaceAll(sweep.SharePost(sweep.Summarize(removed), 1), " 🧹", "")
	m.phase = phaseDone
	return m.View()
}
