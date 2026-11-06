//go:build !nogui

package gui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

func TestTrimShortPreview(t *testing.T) {
	a := trimGUIApp(t)
	file := filepath.Join(t.TempDir(), "preview.mp4")
	trimTestCommand(t, "-f", "lavfi", "-i", "nullsrc=s=160x96:r=10:d=12,geq=lum='16+219*mod(floor(N/pow(2,floor(X/20))),2)':cb=128:cr=128", "-f", "lavfi", "-i", "sine=frequency=440:duration=12", "-c:v", "libx264", "-g", "120", "-c:a", "aac", file)
	a.openTrimSource(file, MediaInfo{Duration: 12 * time.Second}, ModeVideo, pageSetup, nil)
	tr := &a.trim
	tr.media.SetMuted(true)
	trimGUIWait(t, func() bool { return tr.media.IsPrepared() && tr.media.IsSeekable() })
	tr.selection = trimSelection{4 * time.Second, 7 * time.Second, 12 * time.Second, true}
	a.updateTrim()
	a.commitTrimEdit()
	history := len(tr.history.states)
	if !tr.shortCut.Visible() || tr.shortStart.Visible() || tr.shortEnd.Visible() {
		t.Fatal("wrong preview controls for Remove")
	}
	seeks := 0
	inflightPreview, inflightEdit := false, false
	tr.media.NotifyProperty("seeking", func() {
		if tr.media.IsSeeking() {
			seeks++
			if inflightPreview {
				inflightPreview = false
				tr.shortEnd.Emit("clicked")
			}
			if inflightEdit {
				inflightEdit = false
				tr.start.SetValue(3)
			}
		}
	})
	tr.shortCut.Emit("clicked")
	trimGUIWait(t, func() bool { return tr.media.Playing() && tr.media.Timestamp() > 2200000 })
	if marker := trimDisplayedFrame(a); marker < 20 || marker >= 40 {
		t.Fatalf("wrong picture before cut: %d", marker)
	}
	tr.play.Emit("clicked")
	paused := tr.media.Timestamp()
	elapsed := false
	glib.TimeoutAdd(150, func() bool { elapsed = true; return false })
	trimGUIWait(t, func() bool { return elapsed })
	if tr.media.Playing() || tr.media.Timestamp()-paused > 50000 {
		t.Fatal("short preview did not pause")
	}
	tr.play.Emit("clicked")
	trimGUIWait(t, func() bool { return tr.media.Timestamp() > 7300000 && !tr.media.IsSeeking() && !tr.seekQueued })
	if marker := trimDisplayedFrame(a); marker < 70 || marker >= 90 {
		t.Fatalf("wrong picture after cut: %d", marker)
	}
	trimGUIWait(t, func() bool { return !tr.previewing && !tr.media.IsSeeking() && !tr.seekQueued })
	if tr.media.Playing() || tr.media.Timestamp() < 9000000 || tr.media.Timestamp() > 9200000 || seeks != 2 {
		t.Fatalf("cut preview did not stop after one skip: position %d, seeks %d", tr.media.Timestamp(), seeks)
	}
	if len(tr.history.states) != history {
		t.Fatal("preview changed edit history")
	}
	tr.selection = trimSelection{2 * time.Second, 10 * time.Second, 12 * time.Second, false}
	a.updateTrim()
	a.commitTrimEdit()
	if tr.shortCut.Visible() || !tr.shortStart.Visible() || !tr.shortEnd.Visible() {
		t.Fatal("wrong preview controls for Keep")
	}
	tr.shortStart.Emit("clicked")
	tr.preview.Emit("clicked")
	tr.shortEnd.Emit("clicked")
	trimGUIWait(t, func() bool { return !tr.previewing && !tr.seekQueued && !tr.media.IsSeeking() })
	if tr.media.Playing() || tr.media.Timestamp() < 10000000 || tr.media.Timestamp() > 10200000 {
		t.Fatal("latest short preview did not win")
	}
	tr.shortEnd.Emit("clicked")
	tr.start.SetValue(2.5)
	trimGUIWait(t, func() bool { return !tr.seekQueued && !tr.media.IsSeeking() })
	if tr.previewing || tr.playOnSeek || tr.media.Playing() {
		t.Fatal("selection edit resumed an old preview")
	}
	inflightPreview = true
	tr.shortStart.Emit("clicked")
	trimGUIWait(t, func() bool { return !inflightPreview && !tr.previewing && !tr.seekQueued && !tr.media.IsSeeking() })
	if marker := trimDisplayedFrame(a); marker < 98 || marker > 102 {
		t.Fatalf("in-flight replacement showed the wrong end window: %d", marker)
	}
	if tr.media.Playing() || tr.media.Timestamp() < 10000000 || tr.media.Timestamp() > 10200000 {
		t.Fatal("in-flight replacement did not stop at latest end")
	}
	inflightEdit = true
	tr.shortEnd.Emit("clicked")
	trimGUIWait(t, func() bool { return !inflightEdit && !tr.seekQueued && !tr.media.IsSeeking() })
	elapsed = false
	glib.TimeoutAdd(150, func() bool { elapsed = true; return false })
	trimGUIWait(t, func() bool { return elapsed })
	if tr.previewing || tr.playOnSeek || tr.media.Playing() || tr.media.Timestamp() < 3000000 || tr.media.Timestamp() > 3050000 {
		t.Fatal("in-flight edit resumed an obsolete preview")
	}
	tr.shortStart.Emit("clicked")
	a.stopTrim()
	if tr.media != nil || tr.playOnSeek || tr.seekQueued {
		t.Fatal("leaving editor retained pending preview")
	}
}
