//go:build !nogui

package gui

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func TestTrimHistoryInteraction(t *testing.T) {
	a := trimGUIApp(t)
	file := filepath.Join(t.TempDir(), "history.mp4")
	trimTestCommand(t, "-f", "lavfi", "-i", "nullsrc=s=160x96:r=20:d=2,geq=lum=16+N*4:cb=128:cr=128", "-c:v", "libx264", file)
	original, err := os.ReadFile(file) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	a.openTrimSource(file, MediaInfo{Duration: 2 * time.Second}, ModeVideo, pageSetup, nil)
	trimGUIWait(t, func() bool { return a.trim.media.IsPrepared() && a.trim.timeline.Width() > 20 })
	tr := &a.trim
	initial := tr.selection
	model := tr.timeline.ObserveControllers()
	found := false
	for i := uint(0); i < model.NItems(); i++ {
		gesture := model.Item(i)
		if !gesture.IsA(gtk.GTypeGestureDrag) {
			continue
		}
		found = true
		gesture.Emit("drag-begin", float64(10), float64(30))
		for j := range 40 {
			gesture.Emit("drag-update", float64(tr.timeline.Width()-20)*float64(j+1)/160, float64(0))
		}
		gesture.Emit("drag-end", float64(tr.timeline.Width()-20)/4, float64(0))
	}
	if !found || len(tr.history.states) != 2 || tr.selection.start != 500*time.Millisecond {
		t.Fatalf("drag history: %+v, %d", tr.selection, len(tr.history.states))
	}
	tr.playOnSeek = true
	tr.undo.Emit("clicked")
	if tr.selection != initial || tr.start.Value() != 0 || tr.playOnSeek || tr.previewing || tr.undo.Sensitive() || !tr.redo.Sensitive() {
		t.Fatal("undo did not restore the whole editor")
	}
	tr.redo.Emit("clicked")
	if tr.selection.start != 500*time.Millisecond {
		t.Fatal("redo lost drag")
	}
	tr.keep.SetActive(false)
	tr.reset.Emit("clicked")
	tr.undo.Emit("clicked")
	if !tr.selection.remove || tr.selection.start != 500*time.Millisecond || tr.keep.Active() {
		t.Fatal("reset was not undoable")
	}
	tr.undo.Emit("clicked")
	if tr.selection.remove || !tr.keep.Active() {
		t.Fatal("mode was not restored")
	}
	tr.start.GrabFocus()
	trimGUIWait(t, func() bool { return tr.start.StateFlags()&gtk.StateFlagFocusWithin != 0 })
	if a.trimHistoryShortcut(gdk.KEY_z, gdk.ControlMask) {
		t.Fatal("global undo consumed text editing shortcut")
	}
	before := len(tr.history.states)
	tr.start.SetValue(0.7)
	tr.start.SetValue(0.8)
	tr.start.Emit("activate")
	if len(tr.history.states) > before+1 || tr.redo.Sensitive() {
		t.Fatal("timestamp edit was not grouped or retained redo")
	}
	tr.back.GrabFocus()
	if !a.trimHistoryShortcut(gdk.KEY_z, gdk.ControlMask) || tr.selection.start != 500*time.Millisecond {
		t.Fatal("shortcut did not undo committed time edit")
	}
	tr.export = func() {}
	selection := tr.selection
	tr.undo.Emit("clicked")
	if tr.selection != selection {
		t.Fatal("undo changed selection during export")
	}
	tr.export = nil
	tr.end.SetValue(1.5)
	tr.reset.Emit("clicked")
	tr.undo.Emit("clicked")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tr.redo.Emit("clicked")
	tr.undo.Emit("clicked")
	target, info, err := exportTrim(ctx, file, "", tr.selection, nil)
	if err != nil || info.Duration != time.Second {
		t.Fatalf("export after undo: %+v %v", info, err)
	}
	sourceFrames := trimTestCommand(t, "-i", file, "-f", "rawvideo", "-pix_fmt", "gray", "-")
	savedFrames := trimTestCommand(t, "-i", target, "-f", "rawvideo", "-pix_fmt", "gray", "-")
	const pixels = 160 * 96
	if len(savedFrames) != 20*pixels {
		t.Fatalf("saved %d frames, expected 20", len(savedFrames)/pixels)
	}
	for _, pair := range [][2]int{{0, 10}, {19, 29}} {
		var difference float64
		for i := range pixels {
			difference += math.Abs(float64(savedFrames[pair[0]*pixels+i]) - float64(sourceFrames[pair[1]*pixels+i]))
		}
		if difference/pixels > 2 {
			t.Fatalf("saved boundary frame %d differs from source frame %d", pair[0], pair[1])
		}
	}
	data, err := os.ReadFile(file) //nolint:gosec
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("export changed original")
	}
	a.openTrimSource(file, MediaInfo{Duration: 2 * time.Second}, ModeVideo, pageSetup, nil)
	if tr.undo.Sensitive() || tr.redo.Sensitive() {
		t.Fatal("new source inherited history")
	}
}
