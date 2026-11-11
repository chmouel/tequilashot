package keylog

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func event(at time.Duration, typ, code uint16, value int32) []byte {
	ev := make([]byte, eventSize)
	binary.LittleEndian.PutUint64(ev[0:], uint64(at/time.Second))                  //nolint:gosec // test times are positive
	binary.LittleEndian.PutUint64(ev[8:], uint64(at%time.Second/time.Microsecond)) //nolint:gosec // test times are positive
	binary.LittleEndian.PutUint16(ev[16:], typ)
	binary.LittleEndian.PutUint16(ev[18:], code)
	binary.LittleEndian.PutUint32(ev[20:], uint32(value)) //nolint:gosec // the kernel's s32
	return ev
}

func TestHandleKeepsKeyPressesAndReleasesOnly(t *testing.T) {
	var l Logger
	l.handle(event(1*time.Second, evKey, 30, 1))            // a pressed
	l.handle(event(1100*time.Millisecond, evKey, 30, 2))    // auto-repeat
	l.handle(event(1200*time.Millisecond, evKey, 30, 0))    // a released
	l.handle(event(1300*time.Millisecond, evKey, 0x110, 1)) // left mouse button
	l.handle(event(1400*time.Millisecond, 4, 4, 30))        // scan code
	want := []time.Duration{time.Second, 1200 * time.Millisecond}
	if !slices.Equal(l.events, want) {
		t.Fatalf("events = %v, want %v", l.events, want)
	}
}

func TestTimelineDropsPausesAndShiftsLaterKeys(t *testing.T) {
	s := time.Second
	events := []time.Duration{12 * s, 5 * s, 15 * s, 25 * s, 31 * s, 9 * s}
	// Started at 4 s, paused from 10 to 20 s and again from 30 s onwards.
	got := timeline(events, 4*s, []time.Duration{10 * s, 20 * s, 30 * s})
	want := []time.Duration{1 * s, 5 * s, 11 * s}
	if !slices.Equal(got, want) {
		t.Fatalf("timeline = %v, want %v", got, want)
	}
}

func TestSaveAndLoad(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	file := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(file, []byte("recording"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(file); !errors.Is(err, ErrNone) {
		t.Fatalf("Load before Save = %v, want ErrNone", err)
	}
	times := []time.Duration{1500 * time.Millisecond, 1620 * time.Millisecond, 4 * time.Second}
	if err := Save(file, times); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(dir, "renamed.mp4")
	if err := os.Rename(file, renamed); err != nil {
		t.Fatal(err)
	}
	got, err := Load(renamed)
	if err != nil || !slices.Equal(got, times) {
		t.Fatalf("Load after rename = %v, %v; want %v", got, err, times)
	}
	if err := os.WriteFile(renamed, []byte("another recording"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(renamed); !errors.Is(err, ErrNone) {
		t.Fatalf("Load after the file changed = %v, %v; want ErrNone", got, err)
	}
}
