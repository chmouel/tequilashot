//go:build !nogui

package gui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/chmouel/tequilashot/pkg/protocol"
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func trimGUIApp(t *testing.T) *app {
	t.Helper()
	if os.Getenv("TEQUILASHOT_TEST_GUI") != "1" {
		t.Skip("set TEQUILASHOT_TEST_GUI=1 to check GTK interaction")
	}
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	if !gtk.InitCheck() {
		t.Fatal("GTK needs a display")
	}
	adw.Init()
	application := gtk.NewApplication("com.chmouel.Tequilashot.Test", gio.ApplicationNonUnique)
	if err := application.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := &app{pages: gtk.NewStack(), win: adw.NewApplicationWindow(application), toasts: adw.NewToastOverlay()}
	a.pages.AddNamed(gtk.NewBox(gtk.OrientationVertical, 0), pageSetup)
	a.pages.AddNamed(a.buildResult(), pageResult)
	a.pages.AddNamed(a.buildTrim(), pageTrim)
	a.toasts.SetChild(a.pages)
	a.win.SetContent(a.toasts)
	a.win.Present()
	t.Cleanup(func() { a.closing = true; a.cancelTrimImport(); a.stopTrim(); a.win.Destroy() })
	return a
}

func trimGUIWait(t *testing.T, done func() bool) {
	t.Helper()
	loop := glib.NewMainLoop(nil, false)
	deadline := time.Now().Add(12 * time.Second)
	glib.TimeoutAdd(10, func() bool {
		if done() {
			loop.Quit()
			return false
		}
		if time.Now().After(deadline) {
			t.Error("GTK operation timed out")
			loop.Quit()
			return false
		}
		return true
	})
	loop.Run()
	if t.Failed() {
		t.FailNow()
	}
}

func TestTrimImport(t *testing.T) {
	a := trimGUIApp(t)
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.mp4"), filepath.Join(dir, "vidéo 100% #2.mp4")
	for _, file := range []string{first, second} {
		trimTestCommand(t, "-f", "lavfi", "-i", "color=red:s=160x96:r=10:d=2", "-c:v", "libx264", file)
	}
	commands := make(chan string, 20)
	a.b.Send = func(req protocol.Request) (*protocol.Response, error) {
		commands <- req.Action
		return &protocol.Response{State: &protocol.State{}}, nil
	}
	a.pages.SetVisibleChildName(pageSetup)
	a.win.AddController(a.trimDropTarget())
	list := gdk.NewFileListFromArray([]gio.Filer{gio.NewFileForPath(first)})
	if !a.importTrimFiles(list.Files()) {
		t.Fatal("drop file list rejected")
	}
	a.importTrimFiles([]*gio.File{gio.NewFileForURI(gio.NewFileForPath(second).URI())})
	trimGUIWait(t, func() bool { return a.trim.file == second })
	if a.trim.returnPage != pageSetup || a.res.file != "" {
		t.Fatal("import changed the saved result")
	}
	a.trim.selection.start = time.Second
	selection := a.trim.selection
	a.importTrimFiles([]*gio.File{gio.NewFileForPath(filepath.Join(dir, "missing.mp4"))})
	trimGUIWait(t, func() bool { return a.importVideo.cancel == nil })
	if a.trim.selection != selection || a.trim.file != second {
		t.Fatal("failed import replaced the edit")
	}
	if a.importTrimFiles([]*gio.File{gio.NewFileForURI("https://example.com/video.mp4")}) {
		t.Fatal("accepted remote file")
	}
	if a.importTrimFiles([]*gio.File{gio.NewFileForPath(first), gio.NewFileForPath(second)}) {
		t.Fatal("accepted multiple files")
	}
	a.busy = true
	if a.importTrimFiles([]*gio.File{gio.NewFileForPath(first)}) {
		t.Fatal("accepted import during capture")
	}
	a.busy = false
	a.trim.export = func() {}
	if a.importTrimFiles([]*gio.File{gio.NewFileForPath(first)}) {
		t.Fatal("accepted import during export")
	}
	a.trim.export = nil
	a.importTrimFiles([]*gio.File{gio.NewFileForPath(first)})
	trimGUIWait(t, func() bool { return a.res.dialogOpen })
	a.win.VisibleDialog().Emit("response", "cancel")
	trimGUIWait(t, func() bool { return !a.res.dialogOpen })
	if a.trim.selection != selection || a.trim.file != second {
		t.Fatal("declined replacement changed edit")
	}
	a.importTrimFiles([]*gio.File{gio.NewFileForPath(first)})
	trimGUIWait(t, func() bool { return a.res.dialogOpen })
	a.win.VisibleDialog().Emit("response", "open")
	trimGUIWait(t, func() bool { return a.trim.file == first })
	if a.trim.returnPage != pageSetup {
		t.Fatal("replacement lost return page")
	}
	a.trim.selection.start = 0
	a.importTrimFiles([]*gio.File{gio.NewFileForPath(second)})
	a.cancelTrimImport()
	if a.trim.file != first {
		t.Fatal("cancelled import replaced source")
	}
	for len(commands) > 0 {
		if command := <-commands; command != "waybar-status" {
			t.Errorf("import sent recorder action %s", command)
		}
	}
}
