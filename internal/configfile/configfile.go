package configfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Write atomically replaces path with data. If the target exists its mode is
// preserved; otherwise 0644 is used. Missing parent directories are created
// (0755). A tempfile in the same directory is used as the staging target; on
// any pre-rename error it is removed.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("configfile: mkdir %s: %w", dir, err)
	}

	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("configfile: stat %s: %w", path, err)
	}

	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("configfile: create temp in %s: %w", dir, err)
	}
	tmp := f.Name()
	cleanup := func() { _ = os.Remove(tmp) }

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("configfile: write temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("configfile: sync temp: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("configfile: close temp: %w", err)
	}
	if err := os.Chmod(tmp, mode); err != nil {
		cleanup()
		return fmt.Errorf("configfile: chmod temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		cleanup()
		return fmt.Errorf("configfile: rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// Backup copies path to path+".bak", overwriting any existing backup. Returns
// ("", nil) if path does not exist.
func Backup(path string) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("configfile: open %s: %w", path, err)
	}
	defer func() { _ = src.Close() }()

	info, err := src.Stat()
	if err != nil {
		return "", fmt.Errorf("configfile: stat %s: %w", path, err)
	}

	data, err := io.ReadAll(src)
	if err != nil {
		return "", fmt.Errorf("configfile: read %s: %w", path, err)
	}

	bak := path + ".bak"
	if err := Write(bak, data); err != nil {
		return "", err
	}
	if err := os.Chmod(bak, info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("configfile: chmod %s: %w", bak, err)
	}
	return bak, nil
}

// Lock acquires an exclusive flock(2) on a sibling "<path>.lock" file. It
// never locks the target itself because rename(2) would invalidate the lock
// fd. Blocks until the lock is acquired. The returned unlock function is
// idempotent and safe to call from any goroutine.
func Lock(path string) (func(), error) {
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("configfile: mkdir %s: %w", filepath.Dir(lockPath), err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("configfile: open lock %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("configfile: flock %s: %w", lockPath, err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		})
	}, nil
}
