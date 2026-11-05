package gui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProbeImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.png")
	f, err := os.Create(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 17, 29))); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := ProbeImage(path)
	if err != nil || info.Width != 17 || info.Height != 29 || info.Size != st.Size() || info.Duration != 0 {
		t.Fatalf("unexpected image details: %+v, %v", info, err)
	}
	if _, err := ProbeImage(path + ".missing"); err == nil {
		t.Fatal("expected an error for a missing image")
	}
	if err := os.WriteFile(path, []byte("invalid PNG"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ProbeImage(path); err == nil {
		t.Fatal("expected an error for an invalid PNG")
	}
}

func TestRenameScreenshot(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "capture.png")
	for _, name := range []string{"renamed", "renamed.png"} {
		if got, err := RenameTarget(old, name); err != nil || got != filepath.Join(dir, "renamed.png") {
			t.Fatalf("PNG extension was not preserved: %q, %v", got, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "taken.png"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RenameTarget(old, "taken"); err == nil {
		t.Fatal("an existing PNG must not be overwritten")
	}
}

func TestParseProbe(t *testing.T) {
	got, err := ParseProbe([]byte(`{"streams":[{"width":1920,"height":1080}],"format":{"duration":"42.480000"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != 1920 || got.Height != 1080 || got.Duration != 42480*time.Millisecond {
		t.Fatalf("unexpected %+v", got)
	}
	if _, err := ParseProbe([]byte(`nope`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if _, err := ParseProbe([]byte(`{"format":{"duration":"abc"}}`)); err == nil {
		t.Fatal("expected error for invalid duration")
	}
	if got, err := ParseProbe([]byte(`{"streams":[],"format":{}}`)); err != nil || got.Duration != 0 {
		t.Fatalf("empty probe: %+v %v", got, err)
	}
}

func TestFormatDuration(t *testing.T) {
	for in, want := range map[time.Duration]string{
		0:                        "0:00",
		42400 * time.Millisecond: "0:42",
		59600 * time.Millisecond: "1:00",
		75 * time.Second:         "1:15",
		time.Hour + 2*time.Minute + 3*time.Second: "1:02:03",
	} {
		if got := FormatDuration(in); got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatSize(t *testing.T) {
	for in, want := range map[int64]string{
		0:                "0 B",
		1023:             "1023 B",
		1536:             "1.5 KB",
		12 * 1024 * 1024: "12 MB",
		5 << 30:          "5.0 GB",
	} {
		if got := FormatSize(in); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestCaption(t *testing.T) {
	m := MediaInfo{Duration: 42 * time.Second, Width: 1920, Height: 1080, Size: 12 << 20}
	if got, want := m.Caption(), "0:42 · 1920×1080 · 12 MB"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFrameOffset(t *testing.T) {
	if got := FrameOffset(10 * time.Second); got != time.Second {
		t.Errorf("got %v", got)
	}
	if got := FrameOffset(600 * time.Millisecond); got != 300*time.Millisecond {
		t.Errorf("got %v", got)
	}
}

func TestRenameTarget(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "a.mp4")
	if err := os.WriteFile(filepath.Join(dir, "taken.mp4"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"b":     filepath.Join(dir, "b.mp4"),
		"b.mp4": filepath.Join(dir, "b.mp4"),
		" c d ": filepath.Join(dir, "c d.mp4"),
		"a":     old,
	} {
		got, err := RenameTarget(old, name)
		if err != nil || got != want {
			t.Errorf("RenameTarget(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, bad := range []string{"", "  ", "..", "x/y", "taken", "taken.mp4"} {
		if _, err := RenameTarget(old, bad); err == nil {
			t.Errorf("RenameTarget(%q) should fail", bad)
		}
	}
}
