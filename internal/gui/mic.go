//go:build !nogui

package gui

import (
	"context"
	"log"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/chmouel/tequilashot/internal/external"
)

// listMics returns the capture devices and the id of the default input, which
// may be empty. The demo build swaps it for staged content.
var listMics = func(ctx context.Context) ([]external.AudioDevice, string, error) {
	devices, err := external.ListMicrophones(ctx)
	if err != nil {
		return nil, "", err
	}
	return devices, external.DefaultMicrophone(ctx), nil
}

// buildMicRow builds the revealer holding the microphone selector.
func (a *app) buildMicRow() *gtk.Revealer {
	a.micRow = adw.NewComboRow()
	a.micRow.SetTitle("Microphone")
	a.micRow.SetSubtitleLines(1)
	a.micRow.NotifyProperty("selected", a.onMicSelected)

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.AddCSSClass("boxed-list")
	list.Append(a.micRow)

	rev := gtk.NewRevealer()
	rev.SetTransitionType(gtk.RevealerTransitionTypeSlideDown)
	rev.SetChild(list)
	a.micRevealer = rev
	a.mic.ConnectToggled(a.syncMicRevealer)
	return rev
}

func (a *app) syncMicRevealer() {
	a.micRevealer.SetRevealChild(a.mic.Active() && len(a.mics) > 0)
}

// probeMics lists microphones off the main thread.
func (a *app) probeMics() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		devices, def, err := listMics(ctx)
		if err != nil {
			log.Printf("microphones: %v", err)
			// Leave the saved choice for the daemon to verify.
			glib.IdleAdd(func() { a.micsKnown = true })
			return
		}
		glib.IdleAdd(func() { a.setMics(devices, def) })
	}()
}

// setMics fills the selector. A chosen device that has gone falls back to the
// system default for this session; the saved choice is replaced only on record.
func (a *app) setMics(devices []external.AudioDevice, defaultID string) {
	a.mics = devices
	a.micsKnown = true
	idx := 0
	for i, d := range devices {
		if d.ID == a.micSel {
			idx = i + 1
		}
	}
	if idx == 0 {
		a.micSel = ""
	}

	a.micDefault = ""
	for _, d := range devices {
		if d.ID == defaultID {
			a.micDefault = d.Name
		}
	}

	labels := append([]string{"System default"}, micLabels(devices)...)
	a.micPopulating = true
	a.micRow.SetModel(gtk.NewStringList(labels))
	a.micRow.SetSelected(uint(idx))
	a.micPopulating = false
	a.syncMicSubtitle()
	a.syncMicRevealer()
}

func (a *app) syncMicSubtitle() {
	if a.micSel == "" {
		a.micRow.SetSubtitle(glib.MarkupEscapeText(a.micDefault))
	} else {
		a.micRow.SetSubtitle("")
	}
}

func (a *app) onMicSelected() {
	if a.micPopulating {
		return
	}
	i := int(a.micRow.Selected())
	if i < 0 || i > len(a.mics) {
		return
	}
	a.micSel = ""
	if i > 0 {
		a.micSel = a.mics[i-1].ID
	}
	a.syncMicSubtitle()
}
