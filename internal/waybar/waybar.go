// Package waybar hides the Waybar status bar whilst the screen is captured
// and puts it back afterwards, so that it does not end up in screenshots and
// recordings.
//
// Waybar toggles its visibility on SIGUSR1, so overlapping captures share one
// hidden state: the bar is hidden when the first capture starts and shown when
// the last one ends.
package waybar

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/chmouel/tequilashot/internal/prefs"
)

// Variables so that tests can do without a real /proc and real processes.
var (
	procRoot = "/proc"
	// pidfdOpen and pidfdSignal address a process through a file descriptor,
	// which keeps naming the same process even if its PID is reused.
	pidfdOpen   = func(pid int) (int, error) { return unix.PidfdOpen(pid, 0) }
	pidfdSignal = func(fd int, sig syscall.Signal) error { return unix.PidfdSendSignal(fd, sig, nil, 0) }
	closeFD     = unix.Close
	sleep       = time.Sleep
	// settle is how long the bar is given to leave the screen before the
	// capture reads its first frame.
	settle = 150 * time.Millisecond
)

// state is shared by every capture in the process; mu guards all of it.
var (
	mu     sync.Mutex
	active = map[*Hider]struct{}{}
	// bars holds a pidfd per hidden Waybar; it is empty when the bar is shown.
	bars []int
	// closed is set at shutdown so that no capture hides the bar afterwards.
	closed bool
)

// Hider is one capture's claim on the hidden bar; Restore releases it. A nil
// Hider is a no-op, so callers need not test for one.
type Hider struct {
	_ byte // distinct claims need distinct addresses, which a zero-size type does not give
}

// ForScreen hides the bar when the preference allows it and Waybar is
// running. It returns nil when there is nothing to hide. Restore must be
// called once the capture ends.
func ForScreen() *Hider {
	if !prefs.Current().HideWaybar {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	if closed {
		return nil
	}
	if len(active) == 0 {
		bars = hide()
		if len(bars) == 0 {
			return nil
		}
		// Held under the lock so that a capture that joins meanwhile also
		// waits for the bar to leave the screen.
		sleep(settle)
	}
	h := &Hider{}
	active[h] = struct{}{}
	return h
}

// Restore releases the claim and shows the bar again once no capture needs it
// hidden. It is safe to call more than once and from more than one goroutine.
func (h *Hider) Restore() {
	if h == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := active[h]; !ok {
		return
	}
	delete(active, h)
	if len(active) == 0 {
		show()
	}
}

// RestoreAll releases every claim, shows the bar if it is hidden and refuses
// any later claim. It is meant for shutdown, so that the bar is not left
// hidden; later calls to Restore do nothing.
func RestoreAll() {
	mu.Lock()
	defer mu.Unlock()
	closed = true
	clear(active)
	show()
}

// hide toggles every Waybar and returns a pidfd for each one that was toggled.
func hide() []int {
	var fds []int
	for _, pid := range running() {
		fd, err := pidfdOpen(pid)
		if err != nil {
			continue
		}
		// The PID may have been reused before the pidfd was opened, so the
		// name is checked again against the process the pidfd refers to.
		if !isWaybar(pid) || pidfdSignal(fd, syscall.SIGUSR1) != nil {
			_ = closeFD(fd)
			continue
		}
		fds = append(fds, fd)
	}
	return fds
}

// show toggles the hidden Waybars back and closes their pidfds. A Waybar that
// has gone is skipped.
func show() {
	for _, fd := range bars {
		_ = pidfdSignal(fd, syscall.SIGUSR1)
		_ = closeFD(fd)
	}
	bars = nil
}

// running returns the process IDs of the Waybar instances on this system.
func running() []int {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || !entry.IsDir() || !isWaybar(pid) {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

// isWaybar reports whether the process with the given ID is named waybar.
func isWaybar(pid int) bool {
	comm, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "comm")) //nolint:gosec // a name from /proc
	return err == nil && strings.TrimSpace(string(comm)) == "waybar"
}
