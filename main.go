// buybytes finds the disk space developers forget about (build folders, agent
// worktrees, old simulators and emulators, AI models, forgotten downloads)
// and lets you keep or remove each one.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/TheNoumanDev/buybytes/internal/demo"
	"github.com/TheNoumanDev/buybytes/internal/sweep"
	"github.com/TheNoumanDev/buybytes/internal/tui"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
)

var version = "0.2.0"

const usage = `buybytes: free the disk space developers forget about

Usage:
  buybytes                      scan, then pick what to remove (interactive)
  buybytes scan [--json]        scan and print what was found
  buybytes clean --ids a,b      remove items by id (see scan --show-ids)
  buybytes clean --recommended  remove everything marked recommended
  buybytes share [--copy|--x]   a fun post about your last cleanup
  buybytes skill install        teach Claude Code / Codex to use buybytes

Flags:
  --min SIZE      hide items smaller than SIZE (default 100MB)
  --json          machine-readable output (for scripts and coding agents)
  --all           scan: also list "your call" items under 300MB
  --show-ids      scan: show each item's id, for clean --ids
  --purge         delete permanently instead of moving to the Trash
  --dry-run       show what would be removed, remove nothing
  --yes           don't ask for confirmation (clean only)

Nothing is removed without your confirmation. By default things go to the
Trash, so space comes back when you empty it (or use --purge).
`

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("buybytes", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	minFlag := fs.String("min", "100MB", "")
	jsonOut := fs.Bool("json", false, "")
	purge := fs.Bool("purge", false, "")
	dryRun := fs.Bool("dry-run", false, "")
	yes := fs.Bool("yes", false, "")
	ids := fs.String("ids", "", "")
	recommended := fs.Bool("recommended", false, "")
	all := fs.Bool("all", false, "")
	showIDs := fs.Bool("show-ids", false, "")
	copyFlag := fs.Bool("copy", false, "")
	xFlag := fs.Bool("x", false, "")
	_ = fs.Parse(args)

	minSize, err := parseSize(*minFlag)
	if err != nil {
		fail("bad --min: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fail("no home folder: %v", err)
	}

	switch cmd {
	case "", "ui":
		if !isatty.IsTerminal(os.Stdout.Fd()) {
			runScan(home, minSize, *jsonOut, false, true, true)
			return
		}
		if err := tui.Run(home, minSize, *purge, *dryRun); err != nil {
			fail("%v", err)
		}
	case "scan":
		runScan(home, minSize, *jsonOut, *recommended, *all, *showIDs)
	case "clean":
		runClean(home, minSize, *ids, *recommended, *purge, *dryRun, *yes, *jsonOut)
	case "share":
		runShare(home, *copyFlag, *xFlag)
	case "skill":
		runSkill(home, fs.Args())
	case "demo-screen": // hidden: renders README screenshots from a made-up machine
		runDemo(fs.Args())
	case "version", "--version":
		fmt.Println("buybytes", version)
	case "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

var blocked []string

// forceColor makes `scan` output colour even when not a terminal (demo screenshots).
var forceColor bool

func scan(home string, minSize int64, quiet bool) []sweep.Item {
	tty := isatty.IsTerminal(os.Stderr.Fd())
	progress := func(f float64, s string) {
		if quiet || !tty {
			return
		}
		if len(s) > 50 {
			s = s[:49] + "…"
		}
		fmt.Fprintf(os.Stderr, "\r\033[K  %s %3d%%  %s", sweep.Bar(f, 30), int(f*100), s)
	}
	start := time.Now()
	env := sweep.NewEnv(home, minSize, progress)
	items := sweep.ScanAll(env, progress)
	blocked = env.Blocked
	if !quiet {
		fmt.Fprintf(os.Stderr, "\r\033[K  scanned %d projects in %s\n", len(env.Projects), time.Since(start).Round(time.Second))
	}
	return items
}

type jsonReport struct {
	FreeBytes        int64        `json:"free_bytes"`
	ReclaimableBytes int64        `json:"reclaimable_bytes"`
	RecommendedBytes int64        `json:"recommended_bytes"`
	SkippedFolders   []string     `json:"skipped_folders,omitempty"`
	Items            []sweep.Item `json:"items"`
}

func runScan(home string, minSize int64, asJSON, onlyRecommended, all, showIDs bool) {
	items := scan(home, minSize, asJSON)
	if onlyRecommended {
		items = filter(items, func(it sweep.Item) bool { return it.Recommended && !it.ReadOnly })
	}
	if asJSON {
		r := jsonReport{FreeBytes: sweep.FreeBytes(home), Items: items, SkippedFolders: blocked}
		for _, it := range items {
			if it.ReadOnly {
				continue
			}
			r.ReclaimableBytes += it.Size
			if it.Recommended {
				r.RecommendedBytes += it.Size
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
		return
	}
	printTable(items, all, showIDs)
}

// smallCutoff: "your call" and info items below this are summarised, not listed.
const smallCutoff = 300 << 20

func printTable(items []sweep.Item, all, showIDs bool) {
	now := time.Now()
	type sec struct {
		name        string
		rows        [][]string
		sizes       []int64
		hidden      int
		hiddenBytes int64
	}
	secs := []*sec{
		{name: "RECOMMENDED · safe to remove: comes back on its own or unused for a while"},
		{name: "YOUR CALL · check before removing"},
		{name: "NEEDS YOU FIRST · buybytes won't touch these: do what VERDICT says, then scan again"},
	}
	color := isatty.IsTerminal(os.Stdout.Fd()) || forceColor
	paint := func(st lipgloss.Style, s string) string {
		if !color {
			return s
		}
		return st.Render(s)
	}
	secColors := []lipgloss.TerminalColor{tui.Green, tui.Amber, tui.Gray}
	var rec, other int64
	for _, it := range items {
		s := secs[1]
		switch {
		case it.ReadOnly:
			s = secs[2]
		case it.Recommended || it.InUse:
			s = secs[0]
		}
		if !it.ReadOnly {
			if it.Recommended {
				rec += it.Size
			} else if !it.InUse {
				other += it.Size
			}
		}
		if !all && s != secs[0] && it.Size < smallCutoff {
			s.hidden++
			s.hiddenBytes += it.Size
			continue
		}
		last := sweep.Ago(it.LastUsed, now)
		if last == "" {
			last = "-"
		}
		r := []string{sweep.HumanSize(it.Size), sweep.Fit(it.Title, 50), it.Verdict, last, it.Detail}
		if showIDs {
			r = append([]string{it.ID}, r...)
		}
		s.rows = append(s.rows, r)
		s.sizes = append(s.sizes, it.Size)
	}
	head := []string{"SIZE", "NAME", "VERDICT", "LAST USED", "ABOUT"}
	if showIDs {
		head = append([]string{"ID"}, head...)
	}
	// One set of column widths for every section, measured in screen cells
	// (emoji are two cells wide, which tabwriter gets wrong).
	widths := make([]int, len(head))
	for _, s := range secs {
		for _, r := range append(s.rows, head) {
			for i, c := range r {
				widths[i] = max(widths[i], ansi.StringWidth(c))
			}
		}
	}
	sizeCol := 0
	if showIDs {
		sizeCol = 1
	}
	// style(i, cell) colours one cell; padding is computed on the plain text.
	line := func(r []string, style func(i int, c string) string) string {
		var b strings.Builder
		b.WriteString("  ")
		for i, c := range r {
			gap := strings.Repeat(" ", widths[i]-ansi.StringWidth(c))
			switch {
			case i == len(r)-1:
				b.WriteString(style(i, c))
			case i == sizeCol: // right-align sizes
				b.WriteString(gap + style(i, c) + "  ")
			default:
				b.WriteString(style(i, c) + gap + "  ")
			}
		}
		return b.String()
	}
	headStyle := func(_ int, c string) string { return paint(tui.ColumnHeader, c) }
	for si, s := range secs {
		if len(s.rows) == 0 && s.hidden == 0 {
			continue
		}
		fmt.Printf("\n%s\n%s\n", paint(lipgloss.NewStyle().Bold(true).Foreground(secColors[si]), s.name), line(head, headStyle))
		for ri, r := range s.rows {
			fmt.Println(line(r, func(i int, c string) string {
				switch i {
				case sizeCol:
					return paint(tui.SizeStyle(s.sizes[ri]), c)
				case sizeCol + 2:
					return paint(tui.VerdictStyle(c), c)
				case sizeCol + 3, sizeCol + 4:
					return paint(tui.Dim, c)
				}
				return c
			}))
		}
		if s.hidden > 0 {
			fmt.Printf("  + %d smaller items under 300 MB (%s), see them with --all\n", s.hidden, sweep.HumanSize(s.hiddenBytes))
		}
	}
	fmt.Printf("\nRecommended: %s · your call: %s\n", sweep.HumanSize(rec), sweep.HumanSize(other))
	if len(blocked) > 0 {
		fmt.Printf("Skipped (macOS privacy kept us out): %s\n", strings.Join(blocked, ", "))
		fmt.Println("  Allow the prompt, or give your terminal Full Disk Access in System Settings › Privacy & Security.")
	}
	fmt.Println("Run `buybytes` to pick what to remove.")
}

func runClean(home string, minSize int64, ids string, recommended, purge, dryRun, yes, asJSON bool) {
	if ids == "" && !recommended {
		fail("clean needs --ids or --recommended")
	}
	want := map[string]bool{}
	for _, id := range strings.Split(ids, ",") {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	items := scan(home, 0, asJSON) // ids may point at items below --min
	var picked []sweep.Item
	for _, it := range items {
		if it.ReadOnly {
			continue
		}
		if want[it.ID] || (recommended && it.Recommended && it.Size >= minSize) {
			picked = append(picked, it)
			delete(want, it.ID)
		}
	}
	for id := range want {
		fmt.Fprintf(os.Stderr, "  no removable item with id %s (already gone or read-only)\n", id)
	}
	if len(picked) == 0 {
		fail("nothing to remove")
	}
	var total int64
	for _, it := range picked {
		total += it.Size
		if !asJSON {
			fmt.Printf("  %8s  %s\n", sweep.HumanSize(it.Size), it.Title)
		}
	}
	verb := "Move to Trash"
	if purge {
		verb = "Permanently delete"
	}
	if !yes && !dryRun {
		fmt.Printf("\n%s %d items (%s)? [y/N] ", verb, len(picked), sweep.HumanSize(total))
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Println("Nothing removed.")
			return
		}
	}
	before := sweep.FreeBytes(home)
	type outRow struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Size  int64  `json:"size_bytes"`
		Error string `json:"error,omitempty"`
	}
	var rows []outRow
	var removed []sweep.Item
	showBar := !asJSON && isatty.IsTerminal(os.Stderr.Fd())
	var done int64
	for n, it := range picked {
		if showBar {
			fmt.Fprintf(os.Stderr, "\r\033[K  %s %3d%%  %d of %d · %s", sweep.Bar(float64(done)/float64(max(total, 1)), 30),
				done*100/max(total, 1), n+1, len(picked), it.Title)
		}
		r := sweep.Remove(home, it, purge, dryRun)
		done += it.Size
		row := outRow{ID: it.ID, Title: it.Title, Size: it.Size}
		if r.Err != nil {
			row.Error = r.Err.Error()
			if !asJSON {
				fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", it.Title, r.Err)
			}
		}
		rows = append(rows, row)
		if r.Err == nil {
			removed = append(removed, it)
		}
	}
	if !dryRun && len(removed) > 0 {
		_ = sweep.SaveLast(home, sweep.Summarize(removed))
	}
	if showBar {
		fmt.Fprintf(os.Stderr, "\r\033[K  %s 100%%\n", sweep.Bar(1, 30))
	}
	freed := sweep.FreeBytes(home) - before
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"dry_run": dryRun, "purge": purge, "selected_bytes": total, "freed_bytes": freed, "results": rows,
		})
		return
	}
	switch {
	case dryRun:
		fmt.Printf("\nDry run: would remove %s. Nothing was touched.\n", sweep.HumanSize(total))
	case purge:
		fmt.Printf("\nFreed %s (measured on disk).\n", sweep.HumanSize(max(freed, 0)))
	default:
		fmt.Printf("\nMoved %s to the Trash. Empty the Trash to get the space back (tool-removed items like simulators are already freed: %s).\n",
			sweep.HumanSize(total), sweep.HumanSize(max(freed, 0)))
	}
}

func filter(items []sweep.Item, keep func(sweep.Item) bool) []sweep.Item {
	var out []sweep.Item
	for _, it := range items {
		if keep(it) {
			out = append(out, it)
		}
	}
	return out
}

func parseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.TrimSuffix(strings.TrimSuffix(s, "B"), "I")
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "K"):
		mult, s = 1<<10, strings.TrimSuffix(s, "K")
	case strings.HasSuffix(s, "M"):
		mult, s = 1<<20, strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "G"):
		mult, s = 1<<30, strings.TrimSuffix(s, "G")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(v * float64(mult)), nil
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "buybytes: "+format+"\n", a...)
	os.Exit(1)
}

func runShare(home string, copyIt, openX bool) {
	c, err := sweep.LoadLast(home)
	if err != nil {
		fail("no cleanup to share yet: run `buybytes` and remove something first")
	}
	post := sweep.SharePost(c, time.Now().UnixNano())
	fmt.Println(post)
	if copyIt {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(post)
		if err := cmd.Run(); err == nil {
			fmt.Fprintln(os.Stderr, "\n(copied)")
		}
	}
	if openX {
		_ = exec.Command("open", sweep.PostURL(post)).Run()
	}
	if !copyIt && !openX {
		fmt.Fprintln(os.Stderr, "\nRun again for a different roast. --copy copies it, --x opens it on X.")
	}
}

func runDemo(args []string) {
	name := "list"
	if len(args) > 0 {
		name = args[0]
	}
	items := demo.Items()
	if name == "scan" {
		forceColor = true
		lipgloss.SetColorProfile(termenv.TrueColor)
		lipgloss.SetHasDarkBackground(true)
		blocked = nil
		printTable(items, false, false)
		return
	}
	fmt.Print(tui.DemoScreen(name, items, 132, 36))
}
