// Package keylog notes when keyboard keys go down and up during a recording,
// so that noise removal can find the clicks in the sound. Only the times are
// kept: which key was pressed is never read beyond telling keys from mouse
// buttons, and never stored.
package keylog

import (
	"cmp"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// evdev input_event on 64-bit Linux: a timeval, then type, code and value.
const (
	eventSize = 24
	evKey     = 1
	// Mouse, joystick and similar buttons share the key event type.
	btnFirst = 0x100
	btnLast  = 0x15f
	// value of an auto-repeated key, which makes no sound of its own.
	keyRepeat = 2
	// _IOW('E', 0xa0, int)
	eviocsclockid = 0x400445a0
)

var deviceGlobs = []string{"/dev/input/by-id/*-event-kbd", "/dev/input/by-path/*-event-kbd"}

// Logger collects key times from every keyboard it could open.
type Logger struct {
	start  time.Duration
	files  []*os.File
	wg     sync.WaitGroup
	mu     sync.Mutex
	events []time.Duration
	// pauses holds the monotonic times the recording was paused and resumed,
	// in turn.
	pauses []time.Duration
}

func monotonic() time.Duration {
	var ts unix.Timespec
	_ = unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts)
	return time.Duration(ts.Nano())
}

// keyboards returns the keyboard event devices, each once.
func keyboards() []string {
	var devices []string
	for _, g := range deviceGlobs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			if dev, err := filepath.EvalSymlinks(m); err == nil && !slices.Contains(devices, dev) {
				devices = append(devices, dev)
			}
		}
	}
	return devices
}

// Start begins noting key times from now. It fails when no keyboard can be
// read, which usually means the user is not in the input group.
func Start() (*Logger, error) {
	l := &Logger{start: monotonic()}
	var firstErr error
	for _, dev := range keyboards() {
		f, err := os.OpenFile(dev, os.O_RDONLY|unix.O_NONBLOCK, 0) //nolint:gosec // fixed device globs
		if err != nil {
			firstErr = cmp.Or(firstErr, err)
			continue
		}
		if err := unix.IoctlSetPointerInt(int(f.Fd()), eviocsclockid, unix.CLOCK_MONOTONIC); err != nil { //nolint:gosec // a file descriptor fits an int
			_ = f.Close()
			firstErr = cmp.Or(firstErr, err)
			continue
		}
		l.files = append(l.files, f)
	}
	if len(l.files) == 0 {
		return nil, cmp.Or(firstErr, errors.New("no keyboard found"))
	}
	for _, f := range l.files {
		l.wg.Go(func() { l.read(f) })
	}
	return l, nil
}

func (l *Logger) read(f *os.File) {
	buf := make([]byte, eventSize*64)
	for {
		n, err := f.Read(buf)
		if err != nil {
			return
		}
		for off := 0; off+eventSize <= n; off += eventSize {
			l.handle(buf[off : off+eventSize])
		}
	}
}

func (l *Logger) handle(ev []byte) {
	typ := binary.LittleEndian.Uint16(ev[16:])
	code := binary.LittleEndian.Uint16(ev[18:])
	value := int32(binary.LittleEndian.Uint32(ev[20:])) //nolint:gosec // the kernel's s32
	if typ != evKey || value == keyRepeat || (code >= btnFirst && code <= btnLast) {
		return
	}
	sec := int64(binary.LittleEndian.Uint64(ev[0:]))  //nolint:gosec // kernel timeval
	usec := int64(binary.LittleEndian.Uint64(ev[8:])) //nolint:gosec // kernel timeval
	l.add(time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond)
}

func (l *Logger) add(at time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, at)
}

// SetPaused tells the logger the recording was paused or resumed; keys
// pressed whilst paused are not in the recording.
func (l *Logger) SetPaused(paused bool) {
	now := monotonic()
	l.mu.Lock()
	defer l.mu.Unlock()
	if paused != (len(l.pauses)%2 == 1) {
		l.pauses = append(l.pauses, now)
	}
}

// Stop closes the keyboards and returns the key times, measured from Start
// on the recording's timeline.
func (l *Logger) Stop() []time.Duration {
	for _, f := range l.files {
		_ = f.Close()
	}
	l.wg.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	return timeline(l.events, l.start, l.pauses)
}

// timeline turns monotonic event times into times on the recording, which
// began at start and stood still during pauses.
func timeline(events []time.Duration, start time.Duration, pauses []time.Duration) []time.Duration {
	slices.Sort(events)
	out := make([]time.Duration, 0, len(events))
	var skipped time.Duration
	p := 0
	for _, at := range events {
		for p+1 < len(pauses) && pauses[p+1] <= at {
			skipped += pauses[p+1] - pauses[p]
			p += 2
		}
		if at < start || (p < len(pauses) && at >= pauses[p]) {
			continue
		}
		out = append(out, at-start-skipped)
	}
	return out
}
