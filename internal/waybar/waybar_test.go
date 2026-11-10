package waybar

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/chmouel/tequilashot/internal/prefs"
)

// fakeProc writes a fake procRoot holding one process per pid, named after
// the comm file contents.
func fakeProc(t *testing.T, processes map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, name := range processes {
		dir := filepath.Join(root, pid)
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "uptime"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// fakeSystem stands in for the pidfd calls. Each opened pidfd is the process
// ID, so a signal is recorded as "pid:signal"; closed records closed pidfds.
type fakeSystem struct {
	mu     sync.Mutex
	sent   []string
	closed []int
	gone   map[int]bool
}

func (f *fakeSystem) signals() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fmt.Sprint(f.sent)
}

// stubProc points the package at a fake procRoot and a fake pidfd layer, so
// that no real process is signalled.
func stubProc(t *testing.T, root string) *fakeSystem {
	t.Helper()
	restore := prefs.Current()
	prefs.Set(prefs.Default())
	t.Cleanup(func() { prefs.Set(restore) })

	fake := &fakeSystem{gone: map[int]bool{}}
	oldRoot, oldOpen, oldSignal, oldClose, oldSleep, oldSettle := procRoot, pidfdOpen, pidfdSignal, closeFD, sleep, settle
	procRoot = root
	pidfdOpen = func(pid int) (int, error) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if fake.gone[pid] {
			return -1, syscall.ESRCH
		}
		return pid, nil
	}
	pidfdSignal = func(fd int, sig syscall.Signal) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if fake.gone[fd] {
			return syscall.ESRCH
		}
		fake.sent = append(fake.sent, fmt.Sprintf("%d:%d", fd, sig))
		return nil
	}
	closeFD = func(fd int) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		fake.closed = append(fake.closed, fd)
		return nil
	}
	sleep = func(time.Duration) {}
	settle = time.Millisecond
	mu.Lock()
	closed = false
	mu.Unlock()
	t.Cleanup(func() {
		RestoreAll()
		procRoot, pidfdOpen, pidfdSignal, closeFD, sleep, settle = oldRoot, oldOpen, oldSignal, oldClose, oldSleep, oldSettle
	})
	return fake
}

func enable(t *testing.T) {
	t.Helper()
	p := prefs.Current()
	p.HideWaybar = true
	prefs.Set(p)
}

func expectSignals(t *testing.T, fake *fakeSystem, want ...string) {
	t.Helper()
	if got := fake.signals(); got != fmt.Sprint(append([]string{}, want...)) {
		t.Fatalf("signals = %s, want %v", got, want)
	}
}

func TestForScreenHidesAndRestoresWaybar(t *testing.T) {
	fake := stubProc(t, fakeProc(t, map[string]string{"400": "waybar", "401": "bash", "402": "waybar"}))
	enable(t)

	hider := ForScreen()
	if hider == nil {
		t.Fatal("ForScreen() = nil, want a hider")
	}
	expectSignals(t, fake, "400:10", "402:10")

	hider.Restore()
	hider.Restore() // restoring twice must not show the bar and hide it again
	expectSignals(t, fake, "400:10", "402:10", "400:10", "402:10")
	if len(fake.closed) != 2 {
		t.Errorf("closed pidfds = %v, want both closed", fake.closed)
	}
}

func TestOverlappingCapturesShareOneToggle(t *testing.T) {
	for name, order := range map[string][2]int{"first released first": {0, 1}, "last released first": {1, 0}} {
		t.Run(name, func(t *testing.T) {
			fake := stubProc(t, fakeProc(t, map[string]string{"400": "waybar"}))
			enable(t)

			hiders := [2]*Hider{ForScreen(), ForScreen()}
			if hiders[0] == nil || hiders[1] == nil {
				t.Fatal("ForScreen() = nil, want a hider each")
			}
			expectSignals(t, fake, "400:10") // the second capture must not toggle it back

			hiders[order[0]].Restore()
			expectSignals(t, fake, "400:10") // still held by the other capture

			hiders[order[1]].Restore()
			expectSignals(t, fake, "400:10", "400:10")
		})
	}
}

func TestRestoreAllShowsBarAndLaterRestoreIsNoOp(t *testing.T) {
	fake := stubProc(t, fakeProc(t, map[string]string{"400": "waybar"}))
	enable(t)

	first, second := ForScreen(), ForScreen()
	RestoreAll()
	expectSignals(t, fake, "400:10", "400:10")

	first.Restore()
	second.Restore()
	RestoreAll() // nothing is left to restore
	expectSignals(t, fake, "400:10", "400:10")

	// A capture that arrives after shutdown must not hide the bar again.
	if late := ForScreen(); late != nil {
		t.Fatal("ForScreen() hid the bar after RestoreAll")
	}
	expectSignals(t, fake, "400:10", "400:10")
}

func TestRestoreSkipsWaybarThatHasGone(t *testing.T) {
	fake := stubProc(t, fakeProc(t, map[string]string{"400": "waybar", "402": "waybar"}))
	enable(t)

	hider := ForScreen()
	fake.gone[400] = true
	hider.Restore()
	expectSignals(t, fake, "400:10", "402:10", "402:10")
	if len(fake.closed) != 2 {
		t.Errorf("closed pidfds = %v, want both closed", fake.closed)
	}
}

func TestForScreenSkipsWaybarThatGoesBeforeItIsOpened(t *testing.T) {
	fake := stubProc(t, fakeProc(t, map[string]string{"400": "waybar"}))
	enable(t)
	fake.gone[400] = true

	if hider := ForScreen(); hider != nil {
		t.Error("ForScreen() = a hider, want nil for a vanished Waybar")
	}
	expectSignals(t, fake)
}

func TestForScreenDoesNothingWithoutConsent(t *testing.T) {
	fake := stubProc(t, fakeProc(t, map[string]string{"400": "waybar"}))

	if hider := ForScreen(); hider != nil {
		t.Error("ForScreen() hid the bar without the preference")
	}
	expectSignals(t, fake)

	enable(t)
	if hider := ForScreen(); hider == nil {
		t.Fatal("ForScreen() = nil, want a hider")
	}
}

func TestForScreenDoesNothingWithoutWaybar(t *testing.T) {
	fake := stubProc(t, fakeProc(t, map[string]string{"400": "swaybar", "401": "bash"}))
	enable(t)

	if hider := ForScreen(); hider != nil {
		t.Error("ForScreen() hid a bar that is not Waybar")
	}
	expectSignals(t, fake)
}

func TestRestoreOnNilHider(t *testing.T) {
	var hider *Hider
	hider.Restore()
}
