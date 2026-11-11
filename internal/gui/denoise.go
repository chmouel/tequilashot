//go:build !nogui

package gui

import (
	"context"
	"errors"
	"fmt"

	"github.com/chmouel/tequilashot/internal/denoise"
	"github.com/chmouel/tequilashot/internal/prefs"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// denoiseTicket records the view that asked for the noise-removal model and
// what to run once it is in place, so a download started from one recording
// never continues against another.
type denoiseTicket struct {
	page      string
	file      string
	gen       int
	selection trimSelection
	then      func()
}

// denoiseTicketCurrent reports whether the ticket still describes the view on
// show.
func (a *app) denoiseTicketCurrent(t denoiseTicket) bool {
	if a.pages.VisibleChildName() != t.page {
		return false
	}
	switch t.page {
	case pageResult:
		return a.res.file == t.file && a.res.gen == t.gen
	case pageTrim:
		trim := &a.trim
		return trim.file == t.file && trim.gen == t.gen && trim.selection == t.selection
	}
	return false
}

// offerDenoiseModel asks before downloading the noise-removal model and runs
// the ticket's continuation once it is in place.
func (a *app) offerDenoiseModel(ticket denoiseTicket) {
	a.denoisePending = &ticket
	if a.denoiseDownloading {
		return
	}
	dlg := adw.NewAlertDialog("Download the noise-removal model?",
		"Removing noise needs a small speech model, a one-off download of about 300 KB to "+denoise.ModelPath()+".")
	dlg.AddResponse("cancel", "Cancel")
	dlg.AddResponse("download", "Download")
	dlg.SetResponseAppearance("download", adw.ResponseSuggested)
	dlg.SetDefaultResponse("download")
	dlg.SetCloseResponse("cancel")
	a.choose(dlg, func(response string) {
		if response != "download" {
			a.denoisePending = nil
			return
		}
		a.denoiseDownloading = true
		a.toast("Downloading the noise-removal model…")
		go func() {
			err := denoise.DownloadModel(context.Background(), nil)
			glib.IdleAdd(func() {
				a.denoiseDownloading = false
				pending := a.denoisePending
				a.denoisePending = nil
				if a.closing || pending == nil {
					return
				}
				if err != nil {
					a.toast("The model could not be downloaded: " + err.Error())
					return
				}
				if !a.denoiseTicketCurrent(*pending) {
					a.toast("The model is ready; please repeat the action on the recording you want")
					return
				}
				pending.then()
			})
		}()
	})
}

// autoDenoise cleans the sound of a freshly saved recording when the
// preference asks for it. The original is kept, and anything that stops the
// cleaning leaves the recording as saved with a word of explanation.
func (a *app) autoDenoise() {
	r := &a.res
	if !prefs.AutoDenoiseEnabled() || r.mode == ModeScreenshot || r.editing || r.cleaning != nil {
		return
	}
	if !denoise.ModelInstalled() {
		a.toast("Noise removal skipped: the model is not installed in Preferences")
		return
	}
	a.denoiseResult()
}

func (a *app) setResultCleaning(busy bool) {
	r := &a.res
	r.editing = busy
	for _, w := range r.busy {
		gtk.BaseWidget(w).SetSensitive(!busy)
	}
	r.progress.SetVisible(busy)
	r.clean.SetSensitive(true)
	if busy {
		r.denoise.SetLabel("Cancel")
		r.denoise.SetIconName("window-close-symbolic")
	} else {
		r.denoise.SetLabel("Remove noise")
		r.denoise.SetIconName("audio-input-microphone-symbolic")
	}
}

// denoiseResult saves a copy of the shown recording with its sound cleaned,
// or cancels the copy under way.
func (a *app) denoiseResult() {
	r := &a.res
	if r.cleaning != nil {
		r.cleaning()
		r.clean.SetSensitive(false)
		r.progress.SetText("Cancelling…")
		return
	}
	if r.editing {
		return
	}
	if !denoise.ModelInstalled() {
		a.offerDenoiseModel(denoiseTicket{
			page: pageResult, file: r.file, gen: r.gen, then: a.denoiseResult,
		})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r.cleaning, r.cleanDone = cancel, done
	a.setResultCleaning(true)
	r.progress.SetFraction(0)
	r.progress.SetText("Removing noise…")
	file, mode := r.file, r.mode
	go func() {
		defer close(done)
		defer cancel()
		target, err := denoise.Export(ctx, file, "", func(fraction float64) {
			glib.IdleAdd(func() {
				if !a.closing && r.cleanDone == done {
					r.progress.SetFraction(fraction)
					r.progress.SetText(fmt.Sprintf("Removing noise… %.0f%%", 100*fraction))
				}
			})
		})
		var info MediaInfo
		if err == nil {
			info, _ = ProbeMedia(ctx, target)
		}
		glib.IdleAdd(func() {
			r.cleaning, r.cleanDone = nil, nil
			if a.closing {
				return
			}
			a.setResultCleaning(false)
			switch {
			case errors.Is(err, context.Canceled):
				a.toast("Noise removal cancelled; the original is kept")
			case err != nil:
				a.toast("Noise could not be removed: " + err.Error())
			default:
				a.showResult(target, info, mode)
				a.toast("Cleaned copy saved; the original is kept")
			}
		})
	}()
}
