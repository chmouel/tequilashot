//go:build !nogui

package gui

import (
	"errors"
	"fmt"
	"time"

	"github.com/chmouel/tequilashot/pkg/protocol"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

const pageReplay = "replay"

func (a *app) buildReplayDuration() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 10)
	box.Append(section("Replay length"))
	row := gtk.NewBox(gtk.OrientationHorizontal, 0)
	row.AddCSSClass("linked")
	row.SetHAlign(gtk.AlignCenter)
	a.replayDuration = map[int]*gtk.ToggleButton{}
	var first *gtk.ToggleButton
	for _, n := range []int{30, 60, 120} {
		b := gtk.NewToggleButtonWithLabel(fmt.Sprintf("%d s", n))
		if first == nil {
			first = b
		} else {
			b.SetGroup(first)
		}
		a.replayDuration[n] = b
		row.Append(b)
	}
	a.replayDuration[a.settings.ReplaySeconds].SetActive(true)
	box.Append(row)
	return box
}

func (a *app) buildReplayControls() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 18)
	box.SetMarginTop(28)
	box.SetMarginBottom(28)
	box.SetMarginStart(24)
	box.SetMarginEnd(24)
	box.Append(label("Replay buffering", "title-2"))
	a.replayCaption = label("", "dim-label")
	a.replayCaption.SetWrap(true)
	box.Append(a.replayCaption)
	hint := label("Save the previous moments whilst buffering continues.", "dim-label")
	hint.SetWrap(true)
	box.Append(hint)
	save := gtk.NewButtonWithLabel("Save replay")
	save.AddCSSClass("suggested-action")
	save.AddCSSClass("pill")
	save.ConnectClicked(func() { a.controlReplay("replay-save") })
	box.Append(save)
	stop := gtk.NewButtonWithLabel("Stop buffering")
	stop.ConnectClicked(func() { a.controlReplay("replay-stop") })
	box.Append(stop)
	return box
}

func (a *app) showReplay(st *protocol.State) {
	a.replayActive = true
	a.replayCaption.SetText(fmt.Sprintf("%s · last %d seconds", st.ReplayOutput, st.ReplaySeconds))
	a.pages.SetVisibleChildName(pageReplay)
	a.win.SetTitle("Instant replay")
	a.stopThumbTimer()
	a.stopPreview()
	a.followReplay()
}

func (a *app) followReplay() {
	// Demo mode has no daemon to confirm buffering, so keep the staged page
	if a.replayTimer != 0 || captureDisabled {
		return
	}
	failures := 0
	a.replayTimer = glib.TimeoutAdd(1000, func() bool {
		if a.closing || !a.replayActive {
			a.replayTimer = 0
			return false
		}
		if a.replayPolling || a.replayBusy {
			return true
		}
		a.replayPolling = true
		go func() {
			st := a.state()
			glib.IdleAdd(func() {
				a.replayPolling = false
				if a.closing {
					return
				}
				if st == nil {
					failures++
					if failures < 3 {
						return
					}
					a.replayActive = false
					if a.pages.VisibleChildName() == pageReplay {
						a.showSetup()
					}
					if a.res.mode == ModeReplay {
						a.res.again.SetLabel("New replay")
					}
					a.toast("The daemon could not be reached to confirm replay buffering.")
					return
				}
				failures = 0
				a.replayActive = st.Replay
				if st.Replay {
					a.replayCaption.SetText(fmt.Sprintf("%s · last %d seconds", st.ReplayOutput, st.ReplaySeconds))
				}
				if !st.Replay {
					if a.pages.VisibleChildName() == pageReplay {
						a.showSetup()
						a.toast("Replay buffering has stopped.")
					}
					if a.res.mode == ModeReplay {
						a.res.again.SetLabel("New replay")
					}
				}
			})
		}()
		return true
	})
}

func (a *app) controlReplay(action string) {
	if a.replayBusy || a.closing {
		return
	}
	if captureDisabled {
		a.toast("Demo mode: capture is disabled")
		return
	}
	a.replayBusy = true
	a.pages.SetSensitive(false)
	if action == "replay-save" {
		a.win.SetVisible(false)
	}
	go func() {
		if action == "replay-save" {
			time.Sleep(hideSettle)
		}
		resp, err := a.b.Send(protocol.Request{Command: "execute", Action: action})
		if err == nil && resp == nil {
			err = errors.New("the daemon did not respond")
		} else if err == nil && !resp.Success {
			err = errors.New(resp.Message)
		}
		var info MediaInfo
		if err == nil && action == "replay-save" {
			if resp.File == "" {
				err = errors.New("the daemon did not report a replay file")
			} else {
				info = probeSavedVideo(resp.File)
			}
		}
		glib.IdleAdd(func() {
			a.replayBusy = false
			a.pages.SetSensitive(true)
			if a.closing {
				return
			}
			a.win.Present()
			if err != nil {
				a.toast("The replay could not be completed: " + err.Error())
				return
			}
			if action == "replay-stop" {
				a.replayActive = false
				a.showSetup()
				return
			}
			a.showResult(resp.File, info, ModeReplay)
		})
	}()
}
