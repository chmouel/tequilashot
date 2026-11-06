//go:build !nogui

package gui

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func TestNameFormStates(t *testing.T) {
	if os.Getenv("TEQUILASHOT_TEST_GUI") != "1" {
		t.Skip("set TEQUILASHOT_TEST_GUI=1 to check GTK interaction")
	}
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	if !gtk.InitCheck() {
		t.Fatal("GTK needs a display")
	}
	adw.Init()

	dir := t.TempDir()
	write(t, filepath.Join(dir, "taken.png"), "x")

	var last Presentation
	form := newNameForm(nameFormOpts{
		Mode: ModeNewFile, Action: ActionSave, Dir: dir, Ext: ".png", Stem: "fresh", AllowOverwrite: true,
	}, func(_ NameCheck, p Presentation) { last = p })
	form.refresh()
	if !last.Enabled || last.Label != "Save" || last.Destructive {
		t.Fatalf("a free name should be offered plainly: %+v", last)
	}

	form.entry.SetText("")
	if last.Enabled || last.Message != "" || form.revealer.RevealChild() {
		t.Fatalf("an empty name disables quietly: %+v", last)
	}

	form.entry.SetText("a/b")
	if last.Enabled || last.Message == "" || !form.status.HasCSSClass("error") {
		t.Fatalf("an invalid name is explained: %+v", last)
	}

	form.entry.SetText("taken")
	if !last.Enabled || last.Label != "Overwrite" || !last.Destructive || !form.revealer.RevealChild() {
		t.Fatalf("a taken name offers to overwrite: %+v", last)
	}

	form.opts.AllowOverwrite = false
	form.entry.SetText("taken.png")
	if last.Enabled || last.Label != "Save" {
		t.Fatalf("a taken name is refused when overwriting is not offered: %+v", last)
	}

	form.entry.SetText("fresh")
	form.setProblem("Regrettably, that name was taken.")
	if form.status.Text() != "Regrettably, that name was taken." {
		t.Fatalf("the problem is shown on open: %q", form.status.Text())
	}
	form.entry.SetText("fresh2")
	if form.status.Text() != "" {
		t.Fatalf("the problem clears once the user edits: %q", form.status.Text())
	}

	form = newNameForm(nameFormOpts{Mode: ModeNewFile, Action: ActionSave, Dir: dir, Ext: ".png", Stem: "x"}, nil)
	form.suggest("from-ai")
	if form.entry.Text() != "from-ai" {
		t.Fatalf("an untouched field takes a suggestion: %q", form.entry.Text())
	}
	form.entry.SetText("mine")
	form.suggest("late-ai")
	if form.entry.Text() != "mine" {
		t.Fatalf("a suggestion must not overwrite typing: %q", form.entry.Text())
	}
}

func TestSlugStem(t *testing.T) {
	for in, want := range map[string]string{
		"holiday-beach\n":      "holiday-beach",
		"`holiday.png`":        "holiday",
		"a/b":                  "a-b",
		"first\nsecond line":   "first",
		"   ":                  "",
		"..":                   "",
		"\"quoted-name.png\" ": "quoted-name",
	} {
		if got := slugStem(in, ".png"); got != want {
			t.Errorf("slugStem(%q) = %q, want %q", in, got, want)
		}
	}
}
