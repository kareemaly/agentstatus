package configfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWrite_CreatesMissingParents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "file.json")
	if err := Write(path, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("contents = %q", string(got))
	}
}

func TestWrite_PreservesMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := Write(path, []byte("new")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 0600", got)
	}
}

func TestWrite_DefaultModeWhenCreating(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	if err := Write(path, []byte("x")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %o, want 0644", got)
	}
}

func TestWrite_CleansTempfileOnRenameFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores write-permission bits")
	}
	dir := t.TempDir()
	// Create subdir where rename will fail: we make it read-only AFTER
	// a tempfile is created is hard; easier path — target dir itself is
	// read-only, which causes CreateTemp (not Rename) to fail. We instead
	// use a distinct approach: point the target at a path whose parent
	// exists but is a regular file so MkdirAll fails too. That wouldn't
	// exercise cleanup. Simplest reliable simulation: invoke Write with
	// the target being an existing directory — Rename will fail because
	// the destination is a non-empty directory.
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	// Put a file inside so rename-over-directory fails on Linux/macOS.
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatalf("sentinel: %v", err)
	}
	err := Write(target, []byte("payload"))
	if err == nil {
		t.Fatalf("Write: expected error renaming over non-empty directory")
	}
	// Ensure no tempfile leaked in dir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() == "target" {
			continue
		}
		t.Fatalf("leftover entry: %s", e.Name())
	}
}

func TestWrite_ConcurrentWithLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	const N = 16
	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			unlock, err := Lock(path)
			if err != nil {
				t.Errorf("Lock: %v", err)
				return
			}
			defer unlock()
			payload, _ := json.Marshal(map[string]int{"writer": i})
			if err := Write(path, payload); err != nil {
				t.Errorf("Write: %v", err)
			}
		}(i)
	}
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var v map[string]int
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatalf("unmarshal (torn write?): %v; bytes=%q", err, string(got))
	}
	if _, ok := v["writer"]; !ok {
		t.Fatalf("unexpected contents: %v", v)
	}
}

func TestBackup_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	original := []byte(`{"original":true}`)
	if err := Write(path, original); err != nil {
		t.Fatalf("Write: %v", err)
	}
	bak, err := Backup(path)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if bak != path+".bak" {
		t.Fatalf("bak path = %s", bak)
	}
	if err := Write(path, []byte(`{"modified":true}`)); err != nil {
		t.Fatalf("Write modified: %v", err)
	}
	restored, err := os.ReadFile(bak)
	if err != nil {
		t.Fatalf("ReadFile bak: %v", err)
	}
	if err := Write(path, restored); err != nil {
		t.Fatalf("restore Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("after restore: got %q want %q", got, original)
	}
}

func TestBackup_MissingSourceReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")
	bak, err := Backup(path)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if bak != "" {
		t.Fatalf("bak = %q, want empty", bak)
	}
}

func TestLock_SerializesWriters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	unlockA, err := Lock(path)
	if err != nil {
		t.Fatalf("Lock A: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		unlockB, err := Lock(path)
		if err != nil {
			t.Errorf("Lock B: %v", err)
			return
		}
		close(acquired)
		unlockB()
	}()

	select {
	case <-acquired:
		t.Fatalf("B acquired while A holds the lock")
	case <-time.After(100 * time.Millisecond):
	}

	unlockA()

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatalf("B never acquired after A unlocked")
	}
}

func TestLock_UnlockIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	unlock()
	unlock() // must not panic
	unlock()
}

func TestLock_UnlockGoroutineSafe(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock()
		}()
	}
	wg.Wait()
}

// Ensure the mechanisms above cooperate: one locker, one writer, shipped off
// under heavy contention.
func TestWrite_UnderLockNoTornBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	const N = 32
	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			unlock, err := Lock(path)
			if err != nil {
				t.Errorf("Lock: %v", err)
				return
			}
			defer unlock()
			payload := fmt.Appendf(nil, `{"writer":%d,"payload":"%s"}`, i, bigString(i))
			if err := Write(path, payload); err != nil {
				t.Errorf("Write: %v", err)
			}
		}(i)
	}
	wg.Wait()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatalf("torn write: %v; bytes=%q", err, string(got))
	}
}

func bigString(seed int) string {
	b := make([]byte, 4096)
	for i := range b {
		b[i] = byte('a' + (seed+i)%26)
	}
	return string(b)
}
