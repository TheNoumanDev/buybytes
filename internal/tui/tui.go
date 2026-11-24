// Package tui is the interactive keep/remove checklist.
package tui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/TheNoumanDev/buybytes/internal/sweep"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// SmallCutoff: outside RECOMMENDED, items below this are folded away until
// the user presses S, so the list isn't overwhelming.
const SmallCutoff = 300 << 20

type phase int

const (
	phaseScanning phase = iota
	phaseList
	phaseConfirm
	phaseRemoving
	phaseDone
	phaseTrashConfirm
	phaseEmptying
)

type (
	progressMsg struct {
		frac  float64
		label string
	}
	scannedMsg struct {
		items    []sweep.Item
		projects int
		took     time.Duration
		blocked  []string
	}
	removedMsg  sweep.Result
	finishedMsg struct{ free int64 }
	emptiedMsg  struct {
		free int64
		err  error
	}
)

type rowKind int

const (
	rowItem rowKind = iota
	rowSection
	rowCategory
	rowNote
)

type row struct {
	kind  rowKind
	text  string
	item  int // index into items for rowItem
	sec   int
	cat   sweep.Category
	color lipgloss.TerminalColor
}

// selectable rows: items, and category headers outside the read-only section
// (ticking a category ticks everything in it).
func (r row) selectable() bool {
	return r.kind == rowItem || (r.kind == rowCategory && r.sec != secReadOnly)
}

const secReadOnly = 2

type model struct {
	home    string
	minSize int64
	mode    sweep.Mode
	purge   bool
	dryRun  bool

	phase    phase
	spin     spinner.Model
	frac     float64
	label    string
	projects int
	took     time.Duration
	blocked  []string

	items     []sweep.Item
	selected  map[int]bool
	rows      []row
	cursor    int // index into rows
	offset    int
	expanded  map[int]bool
	showSmall bool
	width     int
	height    int

	queue        []int
	removing     string
	removeTotal  int64
	removeDone   int64
	removeCount  int
	results      []sweep.Result
	freeBefore   int64
	freeAfter    int64
	trashEmptied bool
	trashErr     error
	post         string
	notice       string
	now          time.Time
}

var (
	accent  = lipgloss.AdaptiveColor{Light: "#6D28D9", Dark: "#A78BFA"}
	green   = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#34D399"}
	amber   = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"}
	red     = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	gray    = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8B8B9A"}
	dim     = lipgloss.NewStyle().Foreground(gray)
	bold    = lipgloss.NewStyle().Bold(true)
	title   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	capKey  = lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#111827"}).Background(accent)
	cursorS = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#6D28D9"))
	orange  = lipgloss.AdaptiveColor{Light: "#C2410C", Dark: "#FB923C"}
	yellow  = lipgloss.AdaptiveColor{Light: "#A16207", Dark: "#FACC15"}
	cyan    = lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#67E8F9"}
	lime    = lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#86EFAC"}
	colHdr  = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(accent)
	banner  = lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#6D28D9"))
)

// sizeStyle colours sizes like heat: red for the biggest, then orange, yellow.
func sizeStyle(b int64) lipgloss.Style {
	st := lipgloss.NewStyle().Bold(true)
	switch {
	case b >= 5<<30:
		return st.Foreground(red)
	case b >= 1<<30:
		return st.Foreground(orange)
	case b >= SmallCutoff:
		return st.Foreground(yellow)
	}
	return st
}

func verdictStyle(v string) lipgloss.Style {
	st := lipgloss.NewStyle().Bold(true)
	switch v {
	case sweep.VerdictRemove:
		return st.Foreground(green)
	case sweep.VerdictProbably:
		return st.Foreground(lime)
	case sweep.VerdictKeep:
		return st.Foreground(cyan)
	case sweep.VerdictCheck:
		return st.Foreground(amber)
	}
	return st.Foreground(gray) // read-only: what to do first
}

// Run starts the interactive UI.
func Run(home string, minSize int64, mode sweep.Mode, purge, dryRun bool) error {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(accent)
	m := &model{
		home: home, minSize: minSize, mode: mode, purge: purge, dryRun: dryRun, spin: sp,
		selected: map[int]bool{}, expanded: map[int]bool{}, now: time.Now(), freeBefore: sweep.FreeBytes(home),
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	go func() {
		start := time.Now()
		progress := func(f float64, s string) { p.Send(progressMsg{f, s}) }
		env := sweep.NewEnvMode(home, minSize, mode, progress)
		items := sweep.Scan(env, mode, progress)
		p.Send(scannedMsg{items: items, projects: len(env.Projects), took: time.Since(start), blocked: env.Blocked})
	}()
	final, err := p.Run()
	if fm, ok := final.(*model); ok && fm.phase >= phaseDone {
		fmt.Print(fm.summary(false))
	}
	return err
}

func (m *model) Init() tea.Cmd { return m.spin.Tick }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case progressMsg:
		if msg.frac >= m.frac {
			m.frac = msg.frac
		}
		m.label = msg.label
	case scannedMsg:
		m.items, m.projects, m.took, m.blocked = msg.items, msg.projects, msg.took, msg.blocked
		for i, it := range m.items {
			if it.Recommended && !it.ReadOnly {
				m.selected[i] = true
			}
		}
		m.buildRows()
		m.phase = phaseList
	case removedMsg:
		m.results = append(m.results, sweep.Result(msg))
		m.removeDone += msg.Item.Size
		return m, m.removeNext()
	case finishedMsg:
		m.freeAfter = msg.free
		m.phase = phaseDone
		if !m.dryRun {
			var removed []sweep.Item
			for _, r := range m.results {
				if r.Err == nil {
					removed = append(removed, r.Item)
				}
			}
			if len(removed) > 0 {
				c := sweep.Summarize(removed)
				_ = sweep.SaveLast(m.home, c)
				m.post = sweep.SharePost(c, time.Now().UnixNano())
			}
		}
	case emptiedMsg:
		m.freeAfter, m.trashErr = msg.free, msg.err
		m.trashEmptied = msg.err == nil
		m.phase = phaseDone
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.phase {
	case phaseScanning, phaseRemoving, phaseEmptying:
		return m, nil
	case phaseDone:
		switch {
		case (s == "e" || s == "E") && m.canEmptyTrash():
			m.phase = phaseTrashConfirm
			return m, nil
		case (s == "c" || s == "C") && m.post != "":
			m.notice = "copied: paste it anywhere"
			if err := copyText(m.post); err != nil {
				m.notice = "couldn't copy: " + err.Error()
			}
			return m, nil
		case (s == "x" || s == "X") && m.post != "":
			m.notice = "opened X in your browser: check it, then press Post"
			if err := exec.Command("open", sweep.PostURL(m.post)).Run(); err != nil {
				m.notice = "couldn't open the browser: " + err.Error()
			}
			return m, nil
		}
		return m, tea.Quit
	case phaseTrashConfirm:
		if s == "y" || s == "Y" {
			m.phase = phaseEmptying
			home, moved := m.home, m.movedPaths()
			return m, func() tea.Msg {
				err := sweep.EmptyFromTrash(home, moved)
				time.Sleep(1500 * time.Millisecond)
				return emptiedMsg{sweep.FreeBytes(home), err}
			}
		}
		m.phase = phaseDone
		return m, nil
	case phaseConfirm:
		switch s {
		case "y", "Y", "enter":
			m.phase = phaseRemoving
			m.queue = nil
			for _, r := range m.rows {
				if r.kind == rowItem && m.selected[r.item] && !m.items[r.item].ReadOnly {
					m.queue = append(m.queue, r.item)
				}
			}
			for i, on := range m.selected { // selected, then folded away with S
				if on && !m.items[i].ReadOnly && !m.inQueue(i) {
					m.queue = append(m.queue, i)
				}
			}
			m.removeTotal, m.removeDone, m.removeCount = m.selectedBytes(), 0, len(m.queue)
			return m, m.removeNext()
		default:
			m.phase = phaseList
		}
		return m, nil
	}
	// list
	switch s {
	case "q", "Q", "esc":
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+u":
		m.move(-m.listHeight() / 2)
	case "pgdown", "ctrl+d":
		m.move(m.listHeight() / 2)
	case "home", "g":
		m.cursor = 0
		m.move(1)
		m.move(-1)
	case "end", "G":
		m.cursor = len(m.rows) - 1
		m.move(-1)
		m.move(1)
	case " ", "x":
		if m.cursor < len(m.rows) && m.rows[m.cursor].kind == rowCategory {
			m.toggleGroup(m.categoryItems(m.cursor))
		} else if i, ok := m.current(); ok && !m.items[i].ReadOnly {
			m.selected[i] = !m.selected[i]
		}
	case "right", "l", "tab":
		if i, ok := m.current(); ok {
			m.expanded[i] = !m.expanded[i]
		}
	case "a", "A":
		m.toggleGroup(m.sectionItems(m.cursor))
	case "s", "S":
		cur, ok := m.current()
		m.showSmall = !m.showSmall
		m.buildRows()
		if ok {
			for r, rw := range m.rows {
				if rw.kind == rowItem && rw.item == cur {
					m.cursor = r
				}
			}
		}
		m.clampScroll()
	case "p", "P":
		m.purge = !m.purge
	case "enter":
		if m.selectedCount() > 0 {
			m.phase = phaseConfirm
		}
	}
	m.clampScroll()
	return m, nil
}

func (m *model) inQueue(i int) bool {
	for _, q := range m.queue {
		if q == i {
			return true
		}
	}
	return false
}

func (m *model) removeNext() tea.Cmd {
	if len(m.queue) == 0 {
		m.removing = ""
		home := m.home
		return func() tea.Msg {
			time.Sleep(1500 * time.Millisecond) // let APFS settle before measuring
			return finishedMsg{sweep.FreeBytes(home)}
		}
	}
	i := m.queue[0]
	m.queue = m.queue[1:]
	it := m.items[i]
	m.removing = it.Title
	home, purge, dry := m.home, m.purge, m.dryRun
	return func() tea.Msg { return removedMsg(sweep.Remove(home, it, purge, dry)) }
}

func (m *model) buildRows() {
	m.rows = nil
	type sec struct {
		name, desc string
		color      lipgloss.TerminalColor
		fold       bool
		keep       func(sweep.Item) bool
	}
	secs := []sec{
		{"RECOMMENDED", "safe to remove: comes back on its own or unused for a while", green, false,
			func(it sweep.Item) bool { return (it.Recommended || it.InUse) && !it.ReadOnly }},
		{"YOUR CALL", "check before removing", amber, true,
			func(it sweep.Item) bool { return !it.Recommended && !it.InUse && !it.ReadOnly }},
		{"NEEDS YOU FIRST", "buybytes won't touch these: do what VERDICT says, then scan again", gray, true,
			func(it sweep.Item) bool { return it.ReadOnly }},
	}
	for si, s := range secs {
		var idx []int
		var total, hiddenBytes int64
		hidden := 0
		for i, it := range m.items {
			if !s.keep(it) {
				continue
			}
			if !it.InUse {
				total += it.Size
			}
			if s.fold && !m.showSmall && it.Size < SmallCutoff {
				hidden++
				hiddenBytes += it.Size
				continue
			}
			idx = append(idx, i)
		}
		if len(idx) == 0 && hidden == 0 {
			continue
		}
		m.rows = append(m.rows, row{kind: rowSection, sec: si, color: s.color,
			text: fmt.Sprintf("%s · %s  %s", s.name, s.desc, sweep.HumanSize(total))})
		cat := sweep.Category("")
		for _, i := range idx {
			if c := m.items[i].Category; c != cat {
				cat = c
				t := string(c)
				if h := sweep.CategoryHint[c]; h != "" {
					t += " (" + h + ")"
				}
				m.rows = append(m.rows, row{kind: rowCategory, sec: si, cat: c, text: t})
			}
			m.rows = append(m.rows, row{kind: rowItem, sec: si, item: i})
		}
		if hidden > 0 {
			m.rows = append(m.rows, row{kind: rowNote, sec: si,
				text: fmt.Sprintf("+ %d smaller items under 300 MB (%s) · press S to show them", hidden, sweep.HumanSize(hiddenBytes))})
		}
	}
	m.cursor = 0
	m.offset = 0
	m.move(1)
	m.move(-1)
}

func (m *model) current() (int, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) || m.rows[m.cursor].kind != rowItem {
		return 0, false
	}
	return m.rows[m.cursor].item, true
}

// toggleGroup ticks every item in idx, or unticks them all if all are ticked.
func (m *model) toggleGroup(idx []int) {
	all := true
	for _, i := range idx {
		if !m.selected[i] {
			all = false
		}
	}
	for _, i := range idx {
		m.selected[i] = !all
	}
}

// categoryItems lists the selectable items under the category row r.
func (m *model) categoryItems(r int) []int {
	var out []int
	for i := r + 1; i < len(m.rows) && m.rows[i].kind == rowItem; i++ {
		it := m.items[m.rows[i].item]
		if !it.ReadOnly && !it.InUse {
			out = append(out, m.rows[i].item)
		}
	}
	return out
}

// move steps the cursor by n selectable rows, skipping headers and notes.
func (m *model) move(n int) {
	if len(m.rows) == 0 {
		return
	}
	step := 1
	if n < 0 {
		step, n = -1, -n
	}
	if n == 0 {
		n = 1
	}
	pos := m.cursor
	for moved := 0; moved < n; {
		next := pos + step
		for next >= 0 && next < len(m.rows) && !m.rows[next].selectable() {
			next += step
		}
		if next < 0 || next >= len(m.rows) {
			break
		}
		pos = next
		moved++
	}
	if !m.rows[pos].selectable() { // started on a header (initial state)
		for pos < len(m.rows)-1 && !m.rows[pos].selectable() {
			pos++
		}
	}
	m.cursor = pos
}

// sectionItems lists the selectable items in the cursor's section.
func (m *model) sectionItems(r int) []int {
	if r < 0 || r >= len(m.rows) {
		return nil
	}
	var out []int
	for _, rw := range m.rows {
		if rw.kind == rowItem && rw.sec == m.rows[r].sec && !m.items[rw.item].ReadOnly && !m.items[rw.item].InUse {
			out = append(out, rw.item)
		}
	}
	return out
}

func (m *model) selectedCount() int {
	n := 0
	for i, ok := range m.selected {
		if ok && !m.items[i].ReadOnly {
			n++
		}
	}
	return n
}

func (m *model) selectedBytes() int64 {
	var t int64
	for i, ok := range m.selected {
		if ok && !m.items[i].ReadOnly {
			t += m.items[i].Size
		}
	}
	return t
}

const topLines = 6 // framed title + keys (4), scan info, column header

func (m *model) listHeight() int {
	return max(m.height-topLines, 3)
}

// rowLines is how many screen lines a row takes (expanded items show paths).
func (m *model) rowLines(r int) int {
	if m.rows[r].kind == rowItem && m.expanded[m.rows[r].item] {
		return 2 + min(len(m.items[m.rows[r].item].Paths), 6)
	}
	return 1
}

func (m *model) clampScroll() {
	if len(m.rows) == 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
		for m.offset > 0 && m.rows[m.offset-1].kind == rowSection {
			m.offset-- // keep the headers above the first item visible
		}
	}
	for {
		lines := 0
		for r := m.offset; r <= m.cursor && r < len(m.rows); r++ {
			lines += m.rowLines(r)
		}
		if lines <= m.listHeight() || m.offset >= m.cursor {
			break
		}
		m.offset++
	}
}

func (m *model) View() string {
	switch m.phase {
	case phaseScanning:
		return m.viewScanning()
	case phaseConfirm:
		return m.viewConfirm()
	case phaseRemoving:
		return m.viewRemoving()
	case phaseDone, phaseTrashConfirm, phaseEmptying:
		return m.summary(true)
	}
	return m.viewList()
}

func (m *model) barWidth() int {
	w := m.width
	if w <= 0 {
		w = 80
	}
	return max(min(40, w-14), 10)
}

func (m *model) progressLine(frac float64) string {
	return "  " + lipgloss.NewStyle().Foreground(accent).Render(sweep.Bar(frac, m.barWidth())) +
		bold.Render(fmt.Sprintf(" %3d%%", int(frac*100)))
}

func (m *model) viewScanning() string {
	var b strings.Builder
	b.WriteString("\n  " + title.Render("buybytes") + dim.Render("  looking for space you forgot about") + "\n\n")
	b.WriteString(m.progressLine(m.frac) + "\n")
	b.WriteString("  " + m.spin.View() + " " + dim.Render(ansi.Truncate(m.label, max(m.width-6, 20), "…")) + "\n")
	return b.String()
}

func (m *model) viewRemoving() string {
	var b strings.Builder
	frac := 1.0
	if m.removeTotal > 0 {
		frac = float64(m.removeDone) / float64(m.removeTotal)
	}
	verb := "moving to the Trash"
	if m.purge {
		verb = "deleting"
	}
	if m.dryRun {
		verb = "dry run"
	}
	b.WriteString("\n  " + title.Render("buybytes") + dim.Render("  "+verb) + "\n\n")
	b.WriteString(m.progressLine(frac) + dim.Render(fmt.Sprintf("   %s of %s", sweep.HumanSize(m.removeDone), sweep.HumanSize(m.removeTotal))) + "\n")
	if m.removing != "" {
		b.WriteString(fmt.Sprintf("  %s %s %s\n", m.spin.View(), ansi.Truncate(m.removing, max(m.width-20, 20), "…"),
			dim.Render(fmt.Sprintf("(%d of %d)", len(m.results)+1, m.removeCount))))
	} else {
		b.WriteString("  " + m.spin.View() + dim.Render(" measuring the space you got back…") + "\n")
	}
	return b.String()
}

func keyBar(keys [][2]string) string {
	var parts []string
	for _, k := range keys {
		parts = append(parts, capKey.Render(k[0])+" "+k[1])
	}
	return strings.Join(parts, "   ")
}

// columns returns the name, verdict and last-used widths for a screen width
// (0 means the column is hidden on narrow screens).
func columns(w int) (nameW, verdW, lastW int) {
	nameW = min(46, max(16, w/3))
	if w >= 70 {
		verdW = 15
	}
	if w >= 90 {
		lastW = 10
	}
	return
}

func (m *model) viewList() string {
	w := m.width
	if w <= 0 {
		w = 100
	}
	if len(m.items) == 0 {
		return "\n  " + title.Render("buybytes") + "  nothing big to clean. Nice.\n\n  " + dim.Render("q to quit") + "\n"
	}
	mode := lipgloss.NewStyle().Foreground(green).Render("move to Trash")
	if m.purge {
		mode = lipgloss.NewStyle().Foreground(red).Bold(true).Render("DELETE PERMANENTLY")
	}
	if m.dryRun {
		mode += dim.Render(" (dry run)")
	}
	mode += dim.Render(" · P to switch")
	head := fmt.Sprintf("%s  free %s · selected %s in %d items · %s",
		banner.Render("buybytes"), bold.Render(sweep.HumanSize(m.freeBefore)),
		lipgloss.NewStyle().Foreground(accent).Bold(true).Render(sweep.HumanSize(m.selectedBytes())), m.selectedCount(), mode)
	keys := keyBar([][2]string{
		{"SPACE", "select (category = all)"}, {"ENTER", "review & remove"}, {"A", "whole section"},
		{"S", "smaller"}, {"→", "paths"}, {"Q", "quit"},
	})
	subText := fmt.Sprintf(" %s mode · scanned %d projects in %s", m.modeName(), m.projects, m.took.Round(time.Second))
	if m.mode == sweep.ModeMedia {
		subText = fmt.Sprintf(" media mode · scanned in %s", m.took.Round(time.Second))
	}
	if len(m.blocked) > 0 {
		var names []string
		for _, b := range m.blocked {
			names = append(names, "~/"+filepath.Base(b))
		}
		subText += " · skipped " + strings.Join(names, ", ") + " (macOS privacy: allow access or grant Full Disk Access)"
	}
	nameW, verdW, lastW := columns(w)
	colHead := "      " + "     " + colHdr.Render("SIZE") + "  " + colHdr.Render("NAME") + strings.Repeat(" ", nameW-4) + "  "
	if verdW > 0 {
		colHead += colHdr.Render("VERDICT") + strings.Repeat(" ", verdW-7) + "  "
	}
	if lastW > 0 {
		colHead += colHdr.Render("LAST USED") + strings.Repeat(" ", lastW-9) + "  "
	}
	colHead += colHdr.Render("ABOUT")
	inner := max(w-4, 10)
	top := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1).Width(max(w-2, 12)).
		Render(ansi.Truncate(head, inner, "…") + "\n" + ansi.Truncate(keys, inner, "…"))

	var lines []string
	used := 0
	for r := m.offset; r < len(m.rows) && used < m.listHeight(); r++ {
		for _, l := range m.renderRow(r, w) {
			if used >= m.listHeight() {
				break
			}
			lines = append(lines, l)
			used++
		}
	}
	for used < m.listHeight() {
		lines = append(lines, "")
		used++
	}
	return top + "\n" + dim.Render(ansi.Truncate(subText, w, "…")) + "\n" + ansi.Truncate(colHead, w, "") + "\n" +
		strings.Join(lines, "\n")
}

func pad(s string, n int) string {
	s = ansi.Truncate(s, n, "…")
	return s + strings.Repeat(" ", max(0, n-ansi.StringWidth(s)))
}

func (m *model) renderRow(r, w int) []string {
	rw := m.rows[r]
	switch rw.kind {
	case rowSection:
		return []string{" " + lipgloss.NewStyle().Foreground(rw.color).Bold(true).Render(ansi.Truncate(rw.text, w-2, "…"))}
	case rowCategory:
		return []string{m.renderCategory(r, w)}
	case rowNote:
		return []string{dim.Italic(true).Render("     " + ansi.Truncate(rw.text, w-6, "…"))}
	}
	it := m.items[rw.item]
	nameW, verdW, lastW := columns(w)
	box := "[ ]"
	switch {
	case it.ReadOnly:
		box = " · "
	case m.selected[rw.item]:
		box = "[x]"
	}
	last := sweep.Ago(it.LastUsed, m.now)
	if last == "" {
		last = "-"
	}
	size := fmt.Sprintf("%9s", sweep.HumanSize(it.Size))
	name := pad(sweep.Fit(it.Title, nameW), nameW)
	fixed := 2 + 3 + 1 + 9 + 2 + nameW + 2
	if verdW > 0 {
		fixed += verdW + 2
	}
	if lastW > 0 {
		fixed += lastW + 2
	}
	detail := ""
	if rest := w - fixed - 1; rest > 8 {
		detail = ansi.Truncate(it.Detail, rest, "…")
	}

	var line string
	if r == m.cursor {
		// One style over plain text, so the highlight covers the whole row.
		plain := "▶ " + box + " " + size + "  " + name + "  "
		if verdW > 0 {
			plain += pad(it.Verdict, verdW) + "  "
		}
		if lastW > 0 {
			plain += pad(last, lastW) + "  "
		}
		plain += detail
		line = cursorS.Render(pad(plain, w))
	} else {
		boxS := box
		if m.selected[rw.item] && !it.ReadOnly {
			boxS = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(box)
		} else if it.ReadOnly {
			boxS = dim.Render(box)
		}
		line = "  " + boxS + " " + sizeStyle(it.Size).Render(size) + "  " + name + "  "
		if verdW > 0 {
			line += verdictStyle(it.Verdict).Render(pad(it.Verdict, verdW)) + "  "
		}
		if lastW > 0 {
			line += dim.Render(pad(last, lastW)) + "  "
		}
		if it.InUse {
			line += lipgloss.NewStyle().Foreground(green).Render(detail)
		} else {
			line += dim.Render(detail)
		}
	}
	out := []string{line}
	if m.expanded[rw.item] {
		out = append(out, dim.Render("        "+ansi.Truncate(it.Detail, w-9, "…")))
		for i, p := range it.Paths {
			if i == 5 && len(it.Paths) > 6 {
				out = append(out, dim.Render(fmt.Sprintf("        … and %d more", len(it.Paths)-5)))
				break
			}
			out = append(out, dim.Render("        "+ansi.Truncate(p, w-9, "…")))
		}
		if len(it.Paths) == 0 && len(it.Command) > 0 {
			out = append(out, dim.Render("        runs: "+strings.Join(it.Command, " ")))
		}
	}
	return out
}

func (m *model) renderCategory(r, w int) string {
	rw := m.rows[r]
	idx := m.categoryItems(r)
	var total int64
	for i := r + 1; i < len(m.rows) && m.rows[i].kind == rowItem; i++ {
		if it := m.items[m.rows[i].item]; !it.InUse { // in-use items stay, so they don't count
			total += it.Size
		}
	}
	box := "   "
	if rw.sec != secReadOnly && len(idx) > 0 {
		n := 0
		for _, i := range idx {
			if m.selected[i] {
				n++
			}
		}
		switch {
		case n == len(idx):
			box = "[x]"
		case n > 0:
			box = "[-]"
		default:
			box = "[ ]"
		}
	}
	size := fmt.Sprintf("%9s", sweep.HumanSize(total))
	text := ansi.Truncate(rw.text, max(w-len(size)-10, 10), "…")
	if r == m.cursor {
		return cursorS.Render(pad("▶ "+box+" "+size+"  "+text, w))
	}
	boxS := box
	if box == "[x]" || box == "[-]" {
		boxS = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(box)
	}
	return "  " + boxS + " " + bold.Render(size) + "  " + bold.Render(text)
}

func (m *model) viewConfirm() string {
	var b strings.Builder
	verb := "Move to Trash"
	if m.purge {
		verb = lipgloss.NewStyle().Foreground(red).Bold(true).Render("PERMANENTLY DELETE")
	}
	b.WriteString("\n  " + title.Render("Ready to clean") + "\n\n")
	n := 0
	for i := range m.items {
		if !m.selected[i] || m.items[i].ReadOnly {
			continue
		}
		n++
		if n <= 15 {
			it := m.items[i]
			b.WriteString(fmt.Sprintf("    %9s  %s\n", sweep.HumanSize(it.Size), it.Title))
		}
	}
	if n > 15 {
		b.WriteString(dim.Render(fmt.Sprintf("    … and %d more\n", n-15)))
	}
	b.WriteString(fmt.Sprintf("\n  %s %d items, %s?\n\n", verb, n, bold.Render(sweep.HumanSize(m.selectedBytes()))))
	b.WriteString(keyBar([][2]string{{"Y", "yes, go ahead"}, {"N", "no, back to the list"}}) + "\n")
	if m.dryRun {
		b.WriteString(dim.Render("\n  dry run: nothing will actually be touched\n"))
	}
	return b.String()
}

func (m *model) movedPaths() []string {
	var out []string
	for _, r := range m.results {
		if r.Err == nil {
			out = append(out, r.Moved...)
		}
	}
	return out
}

func (m *model) trashedBytes() int64 {
	var t int64
	for _, r := range m.results {
		if r.Err == nil && len(r.Moved) > 0 {
			t += r.Item.Size
		}
	}
	return t
}

func (m *model) canEmptyTrash() bool {
	return !m.dryRun && !m.trashEmptied && len(m.movedPaths()) > 0
}

func (m *model) summary(interactive bool) string {
	var b strings.Builder
	var ok int64
	var errs []string
	for _, r := range m.results {
		if r.Err != nil {
			errs = append(errs, fmt.Sprintf("    ✗ %s: %v", r.Item.Title, r.Err))
			continue
		}
		ok += r.Item.Size
	}
	freed := max(m.freeAfter-m.freeBefore, 0)
	trashed := m.trashedBytes()
	b.WriteString("\n  " + title.Render("buybytes") + "\n\n")
	if m.dryRun {
		b.WriteString(fmt.Sprintf("  Dry run: would have removed %s. Nothing was touched.\n", bold.Render(sweep.HumanSize(ok))))
	} else {
		b.WriteString(fmt.Sprintf("  %s freed on disk (measured)\n", lipgloss.NewStyle().Foreground(green).Bold(true).Render(sweep.HumanSize(freed))))
		switch {
		case m.trashEmptied:
			b.WriteString(fmt.Sprintf("  %s removed from the Trash for good\n", bold.Render(sweep.HumanSize(trashed))))
		case trashed > 0:
			b.WriteString(fmt.Sprintf("  %s is in the Trash, so that space comes back once it's emptied\n", bold.Render(sweep.HumanSize(trashed))))
		}
	}
	if m.trashErr != nil {
		errs = append(errs, fmt.Sprintf("    ✗ emptying the Trash: %v", m.trashErr))
	}
	if len(errs) > 0 {
		b.WriteString("\n  Couldn't remove:\n" + strings.Join(errs, "\n") + "\n")
	}
	if interactive {
		b.WriteString("\n")
		switch m.phase {
		case phaseTrashConfirm:
			b.WriteString(fmt.Sprintf("  Permanently delete the %s buybytes just moved to the Trash?\n", bold.Render(sweep.HumanSize(trashed))))
			b.WriteString(dim.Render("  Anything else in your Trash is left alone. This can't be undone.") + "\n\n")
			b.WriteString(keyBar([][2]string{{"Y", "yes, delete"}, {"N", "no"}}) + "\n")
		case phaseEmptying:
			b.WriteString("  " + m.spin.View() + " emptying those items from the Trash…\n")
		default:
			var keys [][2]string
			if m.canEmptyTrash() {
				keys = append(keys, [2]string{"E", fmt.Sprintf("empty them from the Trash now (%s)", sweep.HumanSize(trashed))})
			}
			if m.post != "" {
				keys = append(keys, [2]string{"C", "copy share post"}, [2]string{"X", "post on X"})
				box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 2).MarginLeft(2)
				b.WriteString(dim.Render("  Brag about it:") + "\n" + box.Render(m.post) + "\n\n")
			}
			keys = append(keys, [2]string{"Q", "quit"})
			b.WriteString("  " + keyBar(keys) + "\n")
			if m.notice != "" {
				b.WriteString("\n  " + lipgloss.NewStyle().Foreground(green).Render(m.notice) + "\n")
			}
		}
	}
	b.WriteString("\n")
	return b.String()
}

// Styles shared with the plain `buybytes scan` output.
var (
	Green        lipgloss.TerminalColor = green
	Amber        lipgloss.TerminalColor = amber
	Gray         lipgloss.TerminalColor = gray
	Dim                                 = dim
	ColumnHeader                        = colHdr
	SizeStyle                           = sizeStyle
	VerdictStyle                        = verdictStyle
)

func copyText(s string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}

func (m *model) modeName() string {
	switch m.mode {
	case sweep.ModeMedia:
		return "media"
	case sweep.ModeAll:
		return "all"
	}
	return "developer"
}
