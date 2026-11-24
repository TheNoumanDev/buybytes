package tui

import (
	"strings"
	"testing"

	"github.com/TheNoumanDev/buybytes/internal/sweep"
	tea "github.com/charmbracelet/bubbletea"
)

func testModel() *model {
	m := &model{selected: map[int]bool{}, expanded: map[int]bool{}, width: 120, height: 30}
	items := []sweep.Item{
		{ID: "a", Category: sweep.CatDev, Title: "Xcode DerivedData", Size: 37 << 30, Recommended: true, Paths: []string{"/x/dd"}},
		{ID: "b", Category: sweep.CatAI, Title: "gemma", Size: 25 << 30, Recommended: true},
		{ID: "c", Category: sweep.CatDevices, Title: "Pixel 8", Size: 14 << 30},
		{ID: "d", Category: sweep.CatInfo, Title: "iPhone backups", Size: 40 << 30, ReadOnly: true},
	}
	m.Update(scannedMsg{items: items})
	return m
}

func key(m *model, k string) {
	var msg tea.KeyMsg
	switch k {
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "space":
		msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	m.Update(msg)
}

func TestRecommendedPreselected(t *testing.T) {
	m := testModel()
	if m.phase != phaseList {
		t.Fatal("not in list phase")
	}
	if !m.selected[0] || !m.selected[1] || m.selected[2] || m.selected[3] {
		t.Errorf("selection = %v, want only recommended", m.selected)
	}
	if got := m.selectedBytes(); got != 62<<30 {
		t.Errorf("selected %d bytes", got)
	}
}

func TestNavigationSkipsHeadersAndReadOnlyCantBeSelected(t *testing.T) {
	m := testModel()
	if m.rows[m.cursor].kind != rowCategory {
		t.Fatalf("cursor should start on the first category row, got %+v", m.rows[m.cursor])
	}
	key(m, "down")
	if i, ok := m.current(); !ok || i != 0 {
		t.Fatalf("down should land on the first item, got %v %v", i, ok)
	}
	for range 20 {
		key(m, "down")
	}
	i, ok := m.current()
	if !ok || !m.items[i].ReadOnly {
		t.Fatalf("cursor should end on the read-only item, got %d", i)
	}
	key(m, "space")
	if m.selected[i] {
		t.Error("read-only item became selected")
	}
	key(m, "up")
	key(m, "space")
	if j, _ := m.current(); !m.selected[j] {
		t.Error("space did not select the your-call item")
	}
}

func TestCategoryRowTicksAllItsItems(t *testing.T) {
	m := &model{selected: map[int]bool{}, expanded: map[int]bool{}, width: 120, height: 30}
	m.Update(scannedMsg{items: []sweep.Item{
		{Category: sweep.CatAgents, Title: "wt1", Size: 2 << 30},
		{Category: sweep.CatAgents, Title: "wt2", Size: 1 << 30},
		{Category: sweep.CatAgents, Title: "wt3", Size: 1 << 30},
	}})
	if m.rows[m.cursor].kind != rowCategory {
		t.Fatal("cursor should start on the Git worktrees row")
	}
	key(m, "space")
	if !m.selected[0] || !m.selected[1] || !m.selected[2] {
		t.Fatalf("category tick should select all: %v", m.selected)
	}
	key(m, "down")
	key(m, "down")
	key(m, "space") // untick wt2
	if m.selected[1] || !strings.Contains(m.View(), "[-]") {
		t.Error("unticking one item should leave the category partly ticked")
	}
	m.cursor = 1
	key(m, "up")
	key(m, "space") // partly ticked -> all
	if !m.selected[1] {
		t.Error("ticking a partly ticked category should select everything")
	}
}

func TestSectionToggleAndConfirm(t *testing.T) {
	m := testModel()
	key(m, "a") // cursor in RECOMMENDED: all selected -> deselect section
	if m.selected[0] || m.selected[1] {
		t.Error("a should toggle the whole section off")
	}
	key(m, "enter")
	if m.phase != phaseList {
		t.Error("enter with nothing selected should not confirm")
	}
	key(m, "a")
	key(m, "enter")
	if m.phase != phaseConfirm {
		t.Fatal("enter should open confirmation")
	}
	if v := m.View(); !strings.Contains(v, "Xcode DerivedData") || !strings.Contains(v, "Move to Trash") {
		t.Errorf("confirm view missing content:\n%s", v)
	}
	key(m, "n")
	if m.phase != phaseList {
		t.Error("n should go back to the list")
	}
}

func TestViewRendersAtNarrowWidths(t *testing.T) {
	for _, w := range []int{40, 80, 200} {
		m := testModel()
		m.width, m.height = w, 12
		key(m, "l") // expand details
		v := m.View()
		if !strings.Contains(v, "RECOMMENDED") {
			t.Errorf("width %d: missing section header", w)
		}
		if lines := strings.Count(v, "\n"); lines > m.height+1 {
			t.Errorf("width %d: view is %d lines, taller than the %d-line screen", w, lines, m.height)
		}
	}
}

func TestSmallItemsFoldedAndInUseKept(t *testing.T) {
	m := &model{selected: map[int]bool{}, expanded: map[int]bool{}, width: 120, height: 30}
	m.Update(scannedMsg{items: []sweep.Item{
		{ID: "a", Category: sweep.CatToolchain, Title: "Flutter 3.27.0 (fvm)", Size: 2 << 30, Recommended: true},
		{ID: "b", Category: sweep.CatToolchain, Title: "Flutter 3.35.4 (fvm)", Size: 2 << 30, InUse: true, Detail: "in use by app", Verdict: "keep"},
		{ID: "c", Category: sweep.CatDev, Title: "big venv", Size: 1 << 30},
		{ID: "d", Category: sweep.CatDev, Title: "tiny venv", Size: 120 << 20},
	}})
	if m.selected[1] {
		t.Error("in-use version must start unchecked")
	}
	v := m.View()
	if !strings.Contains(v, "Flutter 3.35.4") || strings.Contains(v, "tiny venv") || !strings.Contains(v, "1 smaller items") {
		t.Errorf("want in-use shown and tiny venv folded:\n%s", v)
	}
	key(m, "a") // all recommended selected -> off
	key(m, "a") // on again, but never the in-use one
	if m.selected[1] || !m.selected[0] {
		t.Errorf("section toggle touched the in-use item: %v", m.selected)
	}
	key(m, "s")
	if !strings.Contains(m.View(), "tiny venv") {
		t.Error("S should show the smaller items")
	}
}

func TestEmptyTrashOnlyOffersWhatWeMoved(t *testing.T) {
	m := testModel()
	m.phase = phaseDone
	if m.canEmptyTrash() {
		t.Error("nothing was moved, nothing to empty")
	}
	m.results = []sweep.Result{{Item: sweep.Item{Title: "x", Size: 1 << 30}, Moved: []string{"/Users/x/.Trash/build"}}}
	if !m.canEmptyTrash() || !strings.Contains(m.View(), "empty them from the Trash") {
		t.Error("should offer to empty the Trash")
	}
	key(m, "e")
	if m.phase != phaseTrashConfirm {
		t.Fatal("e should ask first")
	}
	key(m, "n")
	if m.phase != phaseDone {
		t.Error("n should go back without deleting")
	}
}
