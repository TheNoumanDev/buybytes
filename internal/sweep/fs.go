package sweep

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// DirSize returns the on-disk size of a file or directory (allocated blocks,
// like du), counting hard-linked files once and never following symlinks.
func DirSize(path string) int64 {
	var total int64
	seen := map[uint64]bool{}
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable: skip, keep going
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			if st.Nlink > 1 && !info.IsDir() {
				if seen[uint64(st.Ino)] {
					return nil
				}
				seen[uint64(st.Ino)] = true
			}
			total += int64(st.Blocks) * 512
		} else {
			total += info.Size()
		}
		return nil
	})
	return total
}

// SizeAll sizes many paths concurrently and returns the sum.
func SizeAll(paths []string) int64 {
	var mu sync.Mutex
	var total int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			s := DirSize(p)
			mu.Lock()
			total += s
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	return total
}

// NewestMtime returns the newest modification time among a path and its
// direct children (cheap "last touched" signal).
func NewestMtime(path string) time.Time {
	var newest time.Time
	if fi, err := os.Stat(path); err == nil {
		newest = fi.ModTime()
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return newest
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	return newest
}

// Exists reports whether a path exists (without following a final symlink).
func Exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// IsDir reports whether path is a directory.
func IsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// FreeBytes returns available bytes on the volume holding path.
func FreeBytes(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
