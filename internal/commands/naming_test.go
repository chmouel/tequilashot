package commands

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestValidPNG(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "good.png")
	empty := filepath.Join(dir, "empty.png")
	junk := filepath.Join(dir, "junk.png")
	for path, data := range map[string][]byte{good: buf.Bytes(), empty: nil, junk: []byte("not a png")} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !validPNG(good) {
		t.Error("a real PNG was refused")
	}
	for _, path := range []string{empty, junk, filepath.Join(dir, "missing.png")} {
		if validPNG(path) {
			t.Errorf("%s must not count as saved output", filepath.Base(path))
		}
	}
}

func TestWriteSnapshotIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shots")
	path, err := writeSnapshot(dir, []byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", st.Mode().Perm())
	}
	if filepath.Dir(path) != dir {
		t.Errorf("snapshot outside the save folder: %s", path)
	}
}
