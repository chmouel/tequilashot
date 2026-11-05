package external

import (
	"context"
	"testing"
	"time"
)

// fakeSession is a preview whose process exits only when release is closed.
type fakeSession struct {
	opened  chan struct{}
	release chan struct{}
}

func newFakeSession() *fakeSession {
	return &fakeSession{opened: make(chan struct{}), release: make(chan struct{})}
}

func (f *fakeSession) run(ctx context.Context) {
	close(f.opened)
	<-ctx.Done()
	<-f.release
}

func isClosed(c <-chan struct{}, within time.Duration) bool {
	select {
	case <-c:
		return true
	case <-time.After(within):
		return false
	}
}

func TestPreviewToggleOffOnWaitsForOldProcess(t *testing.T) {
	var g PreviewGate
	old, next := newFakeSession(), newFakeSession()
	p1 := g.Start(time.Second, old.run, func() { t.Error("unexpected failure") })
	if !isClosed(old.opened, time.Second) {
		t.Fatal("first session never opened")
	}
	g.Retire(p1)
	p2 := g.Start(5*time.Second, next.run, func() { t.Error("unexpected failure") })
	if isClosed(next.opened, 100*time.Millisecond) {
		t.Fatal("new session opened the device while the old process was alive")
	}
	close(old.release)
	if !isClosed(next.opened, time.Second) {
		t.Fatal("new session did not open after the old one exited")
	}
	g.Retire(p2)
	close(next.release)
	if !g.Wait(time.Second) {
		t.Fatal("gate did not drain")
	}
}

func TestPreviewGateBlocksRecordingUntilRelease(t *testing.T) {
	var g PreviewGate
	old := newFakeSession()
	p := g.Start(time.Second, old.run, func() {})
	isClosed(old.opened, time.Second)
	g.Retire(p)
	if g.Wait(50 * time.Millisecond) {
		t.Fatal("gate reported clear while the old process was alive")
	}
	close(old.release)
	if !g.Wait(time.Second) {
		t.Fatal("gate stayed blocked after the old process exited")
	}
}

func TestPreviewReleaseTimeoutIsFailure(t *testing.T) {
	var g PreviewGate
	old := newFakeSession()
	p := g.Start(time.Second, old.run, func() {})
	isClosed(old.opened, time.Second)
	g.Retire(p)

	failed := make(chan struct{})
	next := g.Start(50*time.Millisecond, func(context.Context) { t.Error("opened despite timeout") }, func() { close(failed) })
	if !isClosed(failed, time.Second) {
		t.Fatal("timeout did not report failure")
	}
	if !isClosed(next.done, time.Second) {
		t.Fatal("failed session did not finish")
	}
	// The old process still holds the device, so the gate stays closed.
	if g.Wait(20 * time.Millisecond) {
		t.Fatal("gate cleared whilst old process alive")
	}
	close(old.release)
}

func TestPreviewCancelledWhileWaitingDoesNotFail(t *testing.T) {
	var g PreviewGate
	old := newFakeSession()
	p := g.Start(time.Second, old.run, func() {})
	isClosed(old.opened, time.Second)
	g.Retire(p)

	next := g.Start(5*time.Second, func(context.Context) { t.Error("opened") }, func() { t.Error("failed") })
	g.Retire(next)
	if !isClosed(next.done, time.Second) {
		t.Fatal("cancelled session kept waiting")
	}
	close(old.release)
}
