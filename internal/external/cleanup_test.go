package external

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupOldFiles(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-4 * 24 * time.Hour)

	write := func(name string, mtime time.Time) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return path
	}

	oldFile := write("Screenshot_old.png", old)
	recent := write("recording-new.mp4", time.Now())
	hidden := write(".keep", old)
	nested := write(filepath.Join("sub", "old.png"), old)

	outside := filepath.Join(t.TempDir(), "target.png")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(outside, old, old); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.png")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if err := CleanupOldFiles(context.Background(), dir, 3*24*time.Hour); err != nil {
		t.Fatalf("CleanupOldFiles: %v", err)
	}

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("old file should be removed, stat err = %v", err)
	}
	for _, path := range []string{recent, hidden, nested, link, outside} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("%s should be kept: %v", path, err)
		}
	}
}

func TestCleanupOldFilesMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if err := CleanupOldFiles(context.Background(), missing, time.Hour); err != nil {
		t.Fatalf("missing directory should not be an error: %v", err)
	}
}
