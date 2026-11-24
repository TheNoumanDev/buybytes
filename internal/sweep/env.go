package sweep

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Project is a code project found under the home folder.
type Project struct {
	Path       string
	Markers    map[string]bool // marker file names found in the project root
	GitRoot    bool
	LastActive time.Time
}

// Env is shared, read-only context for scanners.
type Env struct {
	Home        string
	Now         time.Time
	Projects    []*Project
	SourceFiles []string // small text files inside projects, for "is this still referenced?" checks
	MinSize     int64
	// Blocked lists folders macOS privacy settings kept us out of (or that
	// hung waiting for a permission prompt). They were skipped, not scanned.
	Blocked []string

	blockedMu sync.Mutex
	readable  map[string]bool
	refOnce   sync.Once
	refText   []refFile
}

type refFile struct{ path, text string }

var projectMarkers = []string{
	"pubspec.yaml", "package.json", "Cargo.toml", "go.mod", "build.gradle", "build.gradle.kts",
	"settings.gradle", "settings.gradle.kts", "Podfile", "Package.swift", "pyproject.toml", "requirements.txt",
}

// Directories never descended into while discovering projects.
var skipDirNames = map[string]bool{
	"node_modules": true, ".git": true, "build": true, ".dart_tool": true, "Pods": true, ".venv": true,
	"venv": true, "target": true, ".gradle": true, ".next": true, ".build": true, "DerivedData": true,
	"__pycache__": true, ".turbo": true, ".nuxt": true, "dist": true, ".cache": true,
}

// Top-level home folders that hold no code projects.
var skipHomeDirs = map[string]bool{
	"Library": true, "Applications": true, "Pictures": true, "Music": true, "Movies": true, "Public": true,
	"Downloads": true,
}

var sourceExts = map[string]bool{
	".py": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".jsx": true, ".json": true,
	".yaml": true, ".yml": true, ".toml": true, ".sh": true, ".md": true, ".swift": true, ".dart": true,
	".go": true, ".rs": true, ".ipynb": true, ".cfg": true, ".env": true, ".txt": true,
}

const (
	maxDepth          = 8
	maxSourceFiles    = 60000
	maxSourceFileSize = 512 << 10
)

// NewEnv discovers projects under home. progress may be nil.
// Project discovery counts as the first DiscoveryShare of the progress bar.
func NewEnv(home string, minSize int64, progress Progress) *Env {
	e := &Env{Home: home, Now: time.Now(), MinSize: minSize}
	if progress == nil {
		progress = func(float64, string) {}
	}
	progress(0, "finding your projects")
	tops, _ := os.ReadDir(home)
	var roots []string
	for _, t := range tops {
		name := t.Name()
		if !t.IsDir() || strings.HasPrefix(name, ".") || skipHomeDirs[name] {
			continue
		}
		if !e.Readable(filepath.Join(home, name)) {
			continue
		}
		roots = append(roots, filepath.Join(home, name))
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	done := 0
	for _, root := range roots {
		wg.Add(1)
		go func(root string) {
			defer wg.Done()
			projects, files := discover(root)
			mu.Lock()
			e.Projects = append(e.Projects, projects...)
			e.SourceFiles = append(e.SourceFiles, files...)
			done++
			progress(DiscoveryShare*float64(done)/float64(len(roots)), fmt.Sprintf("finding your projects · %d found", len(e.Projects)))
			mu.Unlock()
		}(root)
	}
	wg.Wait()
	sort.Slice(e.Projects, func(i, j int) bool { return e.Projects[i].Path < e.Projects[j].Path })
	if len(e.SourceFiles) > maxSourceFiles {
		e.SourceFiles = e.SourceFiles[:maxSourceFiles]
	}
	return e
}

// probeTimeout: macOS can block a read on Desktop/Documents/Downloads until the
// user answers a privacy prompt. Never hang the whole scan on that.
const probeTimeout = 3 * time.Second

// Readable reports whether dir can be listed promptly. Folders that are denied
// or stuck behind a permission prompt are recorded in Blocked and skipped.
func (e *Env) Readable(dir string) bool {
	e.blockedMu.Lock()
	if ok, seen := e.readable[dir]; seen {
		e.blockedMu.Unlock()
		return ok
	}
	e.blockedMu.Unlock()
	ok := e.probe(dir)
	e.blockedMu.Lock()
	if e.readable == nil {
		e.readable = map[string]bool{}
	}
	if _, seen := e.readable[dir]; !seen {
		e.readable[dir] = ok
		if !ok && Exists(dir) {
			e.Blocked = append(e.Blocked, dir)
		}
	}
	e.blockedMu.Unlock()
	return ok
}

func (e *Env) probe(dir string) bool {
	done := make(chan error, 1)
	go func() {
		_, err := os.ReadDir(dir)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || os.IsNotExist(err) {
			return err == nil
		}
	case <-time.After(probeTimeout):
	}
	return false
}

func discover(root string) ([]*Project, []string) {
	var projects []*Project
	var files []string
	base := strings.Count(root, string(os.PathSeparator))
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (skipDirNames[name] || (strings.HasPrefix(name, ".") && name != ".claude")) {
				return fs.SkipDir
			}
			if strings.Count(p, string(os.PathSeparator))-base > maxDepth {
				return fs.SkipDir
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return fs.SkipDir
			}
			if pr := asProject(p); pr != nil {
				projects = append(projects, pr)
			}
			return nil
		}
		if len(files) < maxSourceFiles && sourceExts[strings.ToLower(filepath.Ext(name))] {
			if info, err := d.Info(); err == nil && info.Size() <= maxSourceFileSize {
				files = append(files, p)
			}
		}
		return nil
	})
	return projects, files
}

func asProject(dir string) *Project {
	var markers map[string]bool
	for _, m := range projectMarkers {
		if Exists(filepath.Join(dir, m)) {
			if markers == nil {
				markers = map[string]bool{}
			}
			markers[m] = true
		}
	}
	git := IsDir(filepath.Join(dir, ".git")) || fileExists(filepath.Join(dir, ".git"))
	if markers == nil && !git {
		return nil
	}
	if markers == nil {
		markers = map[string]bool{}
	}
	return &Project{Path: dir, Markers: markers, GitRoot: git, LastActive: projectActivity(dir, markers)}
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// projectActivity estimates when someone last worked in a project: git index /
// HEAD log, marker files, and the project folder itself.
func projectActivity(dir string, markers map[string]bool) time.Time {
	var newest time.Time
	consider := func(p string) {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	for _, p := range []string{".git/index", ".git/logs/HEAD", ".git/HEAD", ".git"} {
		consider(filepath.Join(dir, p))
	}
	for m := range markers {
		consider(filepath.Join(dir, m))
	}
	for _, sub := range []string{"lib", "src", "app", "."} {
		consider(filepath.Join(dir, sub))
	}
	return newest
}

// ReferencedIn returns, for each needle, whether any project source file
// mentions it (case-insensitive). Files are read once and cached. A needle's
// entry in exclude names a folder whose own files don't count (a repo
// mentioning itself is not "in use").
func (e *Env) ReferencedIn(needles []string, exclude map[string]string) map[string]bool {
	e.refOnce.Do(func() {
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 16)
		for _, f := range e.SourceFiles {
			wg.Add(1)
			sem <- struct{}{}
			go func(f string) {
				defer wg.Done()
				defer func() { <-sem }()
				b, err := os.ReadFile(f)
				if err != nil {
					return
				}
				s := strings.ToLower(string(b))
				mu.Lock()
				e.refText = append(e.refText, refFile{f, s})
				mu.Unlock()
			}(f)
		}
		wg.Wait()
	})
	out := map[string]bool{}
	for _, n := range needles {
		ln := strings.ToLower(n)
		if ln == "" {
			continue
		}
		skip := exclude[n]
		for _, t := range e.refText {
			if skip != "" && strings.HasPrefix(t.path, skip+string(os.PathSeparator)) {
				continue
			}
			if strings.Contains(t.text, ln) {
				out[n] = true
				break
			}
		}
	}
	return out
}

// Run executes a command and returns stdout (empty on any error).
func Run(timeout time.Duration, name string, args ...string) []byte {
	if _, err := exec.LookPath(name); err != nil {
		return nil
	}
	cmd := exec.Command(name, args...)
	done := make(chan struct{})
	var out []byte
	go func() {
		out, _ = cmd.Output()
		close(done)
	}()
	select {
	case <-done:
		return out
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil
	}
}
