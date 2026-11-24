package sweep

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// protectedRel are paths (relative to home) that are never removed themselves.
var protectedRel = []string{
	"", "Documents", "Desktop", "Downloads", "Development", "Library", "Pictures", "Movies", "Music",
	"Applications", "Public", ".ssh", ".gnupg", ".config", ".claude", ".agents", ".Trash",
	"Library/Application Support", "Library/Caches", "Library/Developer", "Library/Android",
	"Library/Android/sdk", ".android", ".android/avd", ".gradle", ".cache", ".cache/huggingface",
	".cache/huggingface/hub", ".ollama", ".ollama/models", "fvm", "fvm/versions",
}

// protectedParts: any path containing one of these segments is never touched.
// (Mole nearly cleaned installed skills folders; we refuse them outright.)
var protectedParts = []string{
	"/.claude/skills", "/.agents/skills", "/.ssh", "/.gnupg", "/Keychains", "/MobileSync/Backup",
	"/.claude/projects", "/Library/Mail", "/Library/Messages", "/Library/Photos",
}

// CheckSafe returns an error if path must not be removed.
func CheckSafe(home, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("refusing relative path %q", path)
	}
	clean := filepath.Clean(path)
	if clean != path {
		return fmt.Errorf("refusing unclean path %q", path)
	}
	rel, err := filepath.Rel(home, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("refusing path outside home: %s", path)
	}
	if rel == "." {
		rel = ""
	}
	for _, p := range protectedRel {
		if rel == p {
			return fmt.Errorf("refusing protected folder: %s", path)
		}
	}
	// Inside ~/.claude/projects only single chat files/folders are removable,
	// never a whole project folder and never its memory notes.
	chat := false
	if r, err := filepath.Rel(filepath.Join(home, ".claude", "projects"), clean); err == nil && !strings.HasPrefix(r, "..") {
		parts := strings.Split(r, string(filepath.Separator))
		if len(parts) != 2 || parts[1] == "memory" || parts[0] == "" {
			return fmt.Errorf("refusing Claude Code project or memory folder: %s", path)
		}
		chat = true
	}
	for _, part := range protectedParts {
		if chat && part == "/.claude/projects" {
			continue
		}
		if strings.Contains(clean+"/", part+"/") {
			return fmt.Errorf("refusing protected location: %s", path)
		}
	}
	return nil
}

// Result reports what happened to one item.
type Result struct {
	Item  Item
	Err   error
	Moved []string // trash destinations
}

// Remove deletes an item. With purge=false paths go to the Trash.
func Remove(home string, it Item, purge, dryRun bool) Result {
	r := Result{Item: it}
	if it.ReadOnly {
		r.Err = errors.New("read-only item: manage it yourself")
		return r
	}
	if len(it.Command) > 0 {
		if dryRun {
			return r
		}
		out, err := exec.Command(it.Command[0], it.Command[1:]...).CombinedOutput()
		if err != nil {
			r.Err = fmt.Errorf("%s: %v: %s", strings.Join(it.Command, " "), err, strings.TrimSpace(string(out)))
		}
		return r
	}
	for _, p := range it.Paths {
		if err := CheckSafe(home, p); err != nil {
			r.Err = err
			return r
		}
	}
	if dryRun {
		return r
	}
	for _, p := range it.Paths {
		if !Exists(p) {
			continue
		}
		if purge {
			if err := os.RemoveAll(p); err != nil {
				r.Err = err
				return r
			}
			continue
		}
		dst, err := toTrash(home, p)
		if err != nil {
			r.Err = err
			return r
		}
		if dst != "" {
			r.Moved = append(r.Moved, dst)
		}
	}
	return r
}

func toTrash(home, p string) (string, error) {
	trash := filepath.Join(home, ".Trash")
	_ = os.MkdirAll(trash, 0o700)
	base := filepath.Base(p)
	dst := filepath.Join(trash, base)
	for n := 1; lexists(dst); n++ { // never rename over something already in the Trash
		dst = filepath.Join(trash, fmt.Sprintf("%s %s %d", base, time.Now().Format("15.04.05"), n))
	}
	if err := os.Rename(p, dst); err == nil {
		return dst, nil
	}
	// Different volume or no permission on ~/.Trash: ask Finder.
	script := fmt.Sprintf(`tell application "Finder" to delete (POSIX file %q)`, p)
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return "", fmt.Errorf("could not move %s to Trash: %v %s", p, err, strings.TrimSpace(string(out)))
	}
	return "", nil // Finder picked the name; we don't know where it went
}

// EmptyFromTrash permanently deletes things buybytes itself moved to the
// Trash (the Moved paths of earlier results). The rest of the Trash is left alone.
func EmptyFromTrash(home string, moved []string) error {
	trash := filepath.Join(home, ".Trash")
	for _, p := range moved {
		if filepath.Dir(filepath.Clean(p)) != trash {
			return fmt.Errorf("refusing %s: not directly inside the Trash", p)
		}
	}
	for _, p := range moved {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}

func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
