// Package sweep finds reclaimable disk space on a developer's machine and removes it safely.
package sweep

import (
	"github.com/charmbracelet/x/ansi"

	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Category groups items in the UI and JSON output.
type Category string

const (
	CatAgents    Category = "Git worktrees"
	CatDev       Category = "Dev builds"
	CatToolchain Category = "Toolchains & SDKs"
	CatDevices   Category = "Emulators & simulators"
	CatAI        Category = "AI models"
	CatAIData    Category = "AI chats & app caches"
	CatDownloads Category = "Downloads & media"
	CatCaches    Category = "Package caches"
	CatInfo      Category = "Look at yourself"
)

// CategoryHint is a one-line explanation shown next to each category.
var CategoryHint = map[Category]string{
	CatAgents:    "extra checkouts left by coding agents and parallel work",
	CatAIData:    "old AI chat history, editor and AI app caches",
	CatDev:       "temporary, they build again next time",
	CatToolchain: "SDK versions, reinstall with one command",
	CatDevices:   "re-download or recreate when you need them",
	CatAI:        "model weights, most can be downloaded again",
	CatDownloads: "files you downloaded and probably forgot",
	CatCaches:    "downloaded again when needed",
}

// Progress reports overall progress (0 to 1) and what is happening now.
type Progress func(frac float64, label string)

// Bar draws a plain progress bar of the given width.
func Bar(frac float64, width int) string {
	frac = min(max(frac, 0), 1)
	n := int(frac*float64(width) + 0.5)
	return strings.Repeat("█", n) + strings.Repeat("░", width-n)
}

// CategoryOrder is the display order.
var CategoryOrder = []Category{CatAgents, CatDev, CatToolchain, CatDevices, CatAI, CatAIData, CatDownloads, CatCaches, CatInfo}

// Item is one thing the user can keep or remove.
type Item struct {
	ID          string    `json:"id"`
	Category    Category  `json:"category"`
	Title       string    `json:"title"`
	Detail      string    `json:"detail"`
	Paths       []string  `json:"paths,omitempty"`
	Size        int64     `json:"size_bytes"`
	LastUsed    time.Time `json:"last_used,omitempty"`
	Recommended bool      `json:"recommended"`
	Regenerable bool      `json:"regenerable"`
	// ReadOnly items are shown for awareness only and can never be selected
	// (iPhone backups and similar user data).
	ReadOnly bool `json:"read_only"`
	// InUse items are shown next to their recommended siblings, unchecked, so
	// the user sees what is being kept (e.g. the Flutter version a project pins).
	InUse bool `json:"in_use,omitempty"`
	// Verdict is buybytes's one-word advice: remove, probably remove, keep,
	// check, or for read-only items what the user has to do first.
	Verdict string `json:"verdict"`
	// Command, when set, removes the item with a tool instead of moving paths
	// (e.g. `xcrun simctl runtime delete`). Run as argv, never through a shell.
	Command []string `json:"command,omitempty"`
}

// NewID makes a short, stable id from the category and a key.
func NewID(cat Category, key string) string {
	h := sha1.Sum([]byte(string(cat) + "\x00" + key))
	return hex.EncodeToString(h[:])[:8]
}

// HumanSize formats bytes like du -h.
func HumanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	v := float64(b) / float64(div)
	if v >= 100 {
		return fmt.Sprintf("%.0f %cB", v, "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.1f %cB", v, "KMGTPE"[exp])
}

// Ago renders a last-used time as "today", "12d ago", "3mo ago".
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < 24*time.Hour:
		return "today"
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 730*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/24/30))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/24/365))
	}
}

// DaysSince returns whole days since t (huge if zero).
func DaysSince(t, now time.Time) int {
	if t.IsZero() {
		return 1 << 30
	}
	return int(now.Sub(t).Hours() / 24)
}

// Fit shortens s to n display cells. Paths keep their end ("…/app/build"),
// everything else keeps its start.
func Fit(s string, n int) string {
	w := ansi.StringWidth(s)
	if w <= n || n < 2 {
		return s
	}
	if strings.Contains(s, "/") {
		return ansi.TruncateLeft(s, w-n+1, "…")
	}
	return ansi.Truncate(s, n, "…")
}

// Verdicts shown in the VERDICT column.
const (
	VerdictRemove   = "remove"
	VerdictProbably = "probably remove"
	VerdictKeep     = "keep"
	VerdictCheck    = "check yourself"
)

// DefaultVerdict fills in advice for items whose scanner didn't set one.
func DefaultVerdict(it Item, now time.Time) string {
	switch {
	case it.ReadOnly:
		return "act first"
	case it.InUse:
		return VerdictKeep
	case it.Recommended:
		return VerdictRemove
	case !it.LastUsed.IsZero() && DaysSince(it.LastUsed, now) <= 14:
		return VerdictKeep // used in the last two weeks
	case !it.LastUsed.IsZero() && DaysSince(it.LastUsed, now) > 30 && it.Regenerable:
		return VerdictProbably
	}
	return VerdictCheck
}
