package sweep

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const staleModelDays = 30

type modelCandidate struct {
	item    Item
	needles []string
	exclude string // folder whose own files don't count as references
}

// ScanAIModels lists local model weights (Hugging Face, Ollama, LM Studio and
// checkpoint folders inside projects), flags the ones no project mentions and
// nobody has opened in a while.
func ScanAIModels(e *Env) []Item {
	var cands []modelCandidate
	cands = append(cands, hfModels(e)...)
	cands = append(cands, ollamaModels(e)...)
	cands = append(cands, lmStudioModels(e)...)
	cands = append(cands, projectCheckpoints(e)...)

	var needles []string
	exclude := map[string]string{}
	for _, c := range cands {
		needles = append(needles, c.needles...)
		for _, n := range c.needles {
			if c.exclude != "" {
				exclude[n] = c.exclude
			}
		}
	}
	refs := e.ReferencedIn(needles, exclude)

	var items []Item
	for _, c := range cands {
		it := c.item
		used := false
		for _, n := range c.needles {
			if refs[n] {
				used = true
				break
			}
		}
		if used {
			it.Verdict = VerdictKeep
			it.Detail = "mentioned in your code"
		} else {
			it.Detail = "no project mentions it"
			it.Recommended = DaysSince(it.LastUsed, e.Now) > staleModelDays
		}
		if it.Regenerable {
			it.Detail += " · re-downloadable"
		}
		items = append(items, it)
	}
	return items
}

func hfHub(home string) string {
	if v := os.Getenv("HUGGINGFACE_HUB_CACHE"); v != "" {
		return v
	}
	if v := os.Getenv("HF_HUB_CACHE"); v != "" {
		return v
	}
	if v := os.Getenv("HF_HOME"); v != "" {
		return filepath.Join(v, "hub")
	}
	return filepath.Join(home, ".cache/huggingface/hub")
}

func hfModels(e *Env) []modelCandidate {
	hub := hfHub(e.Home)
	entries, err := os.ReadDir(hub)
	if err != nil {
		return nil
	}
	var out []modelCandidate
	for _, en := range entries {
		kind, rest, ok := strings.Cut(en.Name(), "--")
		if !ok || (kind != "models" && kind != "datasets") || !en.IsDir() {
			continue
		}
		repo := strings.Replace(rest, "--", "/", 1)
		dir := filepath.Join(hub, en.Name())
		name := repo
		if i := strings.Index(repo, "/"); i >= 0 {
			name = repo[i+1:]
		}
		needles := []string{repo}
		if len(name) >= 6 {
			needles = append(needles, name)
		}
		out = append(out, modelCandidate{
			item: Item{
				Category: CatAI, Title: "🤗 " + repo, Paths: []string{dir}, Regenerable: true,
				LastUsed: lastAccess(dir),
			},
			needles: needles,
		})
	}
	return out
}

func ollamaModels(e *Env) []modelCandidate {
	root := os.Getenv("OLLAMA_MODELS")
	if root == "" {
		root = filepath.Join(e.Home, ".ollama/models")
	}
	manifests := filepath.Join(root, "manifests")
	var out []modelCandidate
	_ = filepath.WalkDir(manifests, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		var m struct {
			Layers []struct {
				Digest string `json:"digest"`
				Size   int64  `json:"size"`
			} `json:"layers"`
		}
		if json.Unmarshal(b, &m) != nil {
			return nil
		}
		var size int64
		var last time.Time
		for _, l := range m.Layers {
			size += l.Size
			blob := filepath.Join(root, "blobs", strings.Replace(l.Digest, ":", "-", 1))
			if t := atime(blob); t.After(last) {
				last = t
			}
		}
		rel, _ := filepath.Rel(manifests, p)
		parts := strings.Split(rel, string(os.PathSeparator))
		if len(parts) < 2 {
			return nil
		}
		model := parts[len(parts)-2] + ":" + parts[len(parts)-1]
		if len(parts) >= 3 && parts[len(parts)-3] != "library" {
			model = parts[len(parts)-3] + "/" + model
		}
		out = append(out, modelCandidate{
			item: Item{
				ID:       NewID(CatAI, "ollama:"+model),
				Category: CatAI, Title: "🦙 ollama " + model, Size: size, LastUsed: last, Regenerable: true,
				Command: []string{"ollama", "rm", model},
			},
			needles: []string{strings.Split(model, ":")[0]},
		})
		return nil
	})
	return out
}

func lmStudioModels(e *Env) []modelCandidate {
	var out []modelCandidate
	for _, root := range []string{filepath.Join(e.Home, ".lmstudio/models"), filepath.Join(e.Home, ".cache/lm-studio/models")} {
		dirs, _ := filepath.Glob(filepath.Join(root, "*", "*"))
		for _, d := range dirs {
			if !IsDir(d) {
				continue
			}
			name := filepath.Base(d)
			out = append(out, modelCandidate{
				item: Item{
					Category: CatAI, Title: "LM Studio " + filepath.Base(filepath.Dir(d)) + "/" + name,
					Paths: []string{d}, Regenerable: true, LastUsed: lastAccess(d),
				},
				needles: []string{name},
			})
		}
	}
	return out
}

var checkpointDirs = []string{"ckpts", "checkpoints", "pretrained_weights", "pretrained_models", "weights"}

// projectCheckpoints finds model weight folders sitting inside cloned AI repos.
func projectCheckpoints(e *Env) []modelCandidate {
	var out []modelCandidate
	seen := map[string]bool{}
	for _, p := range e.Projects {
		for _, d := range checkpointDirs {
			full := filepath.Join(p.Path, d)
			if seen[full] || !IsDir(full) {
				continue
			}
			seen[full] = true
			out = append(out, modelCandidate{
				item: Item{
					Category: CatAI, Title: fmt.Sprintf("Weights in %s", tildePath(e.Home, full)),
					Paths: []string{full}, LastUsed: maxTime(lastAccess(full), p.LastActive),
				},
				// The repo's own name is the best signal that something else still uses it.
				needles: []string{filepath.Base(p.Path) + "/"},
				exclude: p.Path,
			})
		}
	}
	return out
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func atime(p string) time.Time {
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Atimespec.Sec, st.Atimespec.Nsec)
	}
	return fi.ModTime()
}

// lastAccess is the newest access time among the largest files under dir
// (weights are read when a model loads), falling back to the newest mtime.
func lastAccess(dir string) time.Time {
	var newest time.Time
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || n > 2000 {
			return nil
		}
		n++
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		if !d.IsDir() && info.Size() > 50<<20 {
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				if t := time.Unix(st.Atimespec.Sec, st.Atimespec.Nsec); t.After(newest) {
					newest = t
				}
			}
		}
		return nil
	})
	return newest
}
