//go:build !nogui

package gui

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/chmouel/tequilashot/internal/external"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

const bubbleAppID = "com.chmouel.Tequilashot.CameraBubble"

// BubbleOptions describes the round camera shown on screen during a recording.
type BubbleOptions struct {
	Device   string
	X, Y     int // top-left corner in global layout coordinates
	Diameter int
	Width    int // capture mode
	Height   int
	FPS      int
	Mirror   bool
}

// bubbleCSS draws a transparent window holding a circular picture with a ring.
const bubbleCSS = `
window.easyshot-bubble, window.easyshot-bubble.background { background: transparent; box-shadow: none; }
.easyshot-bubble-disc {
	border-radius: 9999px;
	border: %dpx solid rgba(255, 255, 255, 0.92);
	background: #1e1e1e;
}
.easyshot-bubble-disc picture.mirrored { transform: scale(-1, 1); }
`

// RunCameraBubble shows the camera as a click-through circle until it is sent
// SIGTERM or SIGINT. It prints "ready" on stdout once the first picture is on
// screen, and fails if the camera cannot be opened.
func RunCameraBubble(opts BubbleOptions) error {
	// The recorder preloads gtk4-layer-shell for us; ffmpeg must not inherit it.
	_ = os.Unsetenv("LD_PRELOAD")
	if !external.ValidCameraDevice(opts.Device) || opts.Diameter <= 0 {
		return fmt.Errorf("invalid camera bubble options")
	}
	if err := loadLayerShell(); err != nil {
		return err
	}
	if opts.Width <= 0 || opts.Height <= 0 {
		opts.Width, opts.Height = 640, 480
	}
	if opts.FPS <= 0 {
		opts.FPS = 30
	}

	app := gtk.NewApplication(bubbleAppID, gio.ApplicationNonUnique)
	var gate external.PreviewGate
	var preview *external.Preview
	var failure error

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)
	go func() {
		<-sigs
		glib.IdleAdd(app.Quit)
	}()

	fail := func(err error) {
		if failure == nil {
			failure = err
		}
		app.Quit()
	}

	app.ConnectActivate(func() {
		if !layerShellSupported() {
			fail(errors.New("the compositor does not support the layer shell needed for the round camera"))
			return
		}
		win, pic, err := buildBubble(app, opts)
		if err != nil {
			fail(err)
			return
		}

		latest := &external.LatestFrame{}
		ready := false
		preview = external.StartPreview(&gate, opts.Device, opts.Width, opts.Height, opts.FPS,
			func(frame []byte) {
				if !latest.Put(frame) {
					return
				}
				glib.IdleAdd(func() {
					data := latest.Take()
					if data == nil {
						return
					}
					tex, err := gdk.NewTextureFromBytes(glib.NewBytes(data))
					if err != nil {
						return
					}
					pic.SetPaintable(tex)
					if !ready {
						ready = true
						win.SetVisible(true)
						// Give the compositor a moment to show the first picture.
						glib.TimeoutAdd(150, func() bool {
							fmt.Println("ready")
							return false
						})
					}
				})
			},
			func() {
				glib.IdleAdd(func() { fail(fmt.Errorf("camera %s could not be opened", opts.Device)) })
			})
	})

	code := app.Run([]string{os.Args[0]})
	gate.Retire(preview)
	if !gate.Wait(previewRelease) {
		log.Print("camera did not close in time")
	}
	if failure != nil {
		return failure
	}
	if code != 0 {
		return fmt.Errorf("camera bubble exited with status %d", code)
	}
	return nil
}

// buildBubble creates the hidden bubble window on the monitor holding its centre.
func buildBubble(app *gtk.Application, opts BubbleOptions) (*gtk.Window, *gtk.Picture, error) {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return nil, nil, errors.New("no display available")
	}
	ring := max(2, opts.Diameter/90)
	css := gtk.NewCSSProvider()
	css.LoadFromString(fmt.Sprintf(bubbleCSS, ring))
	gtk.StyleContextAddProviderForDisplay(display, css, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

	pic := gtk.NewPicture()
	pic.SetContentFit(gtk.ContentFitCover)
	pic.SetCanShrink(true)
	if opts.Mirror {
		pic.AddCSSClass("mirrored")
	}

	disc := gtk.NewBox(gtk.OrientationVertical, 0)
	disc.AddCSSClass("easyshot-bubble-disc")
	disc.SetOverflow(gtk.OverflowHidden)
	disc.SetSizeRequest(opts.Diameter, opts.Diameter)
	pic.SetVExpand(true)
	disc.Append(pic)

	win := gtk.NewWindow()
	win.SetApplication(app)
	win.AddCSSClass("easyshot-bubble")
	win.SetDecorated(false)
	win.SetResizable(false)
	win.SetDefaultSize(opts.Diameter, opts.Diameter)
	win.SetChild(disc)

	monitor, left, top := monitorAt(display, opts.X, opts.Y, opts.Diameter)
	if err := placeLayer(win, monitor, left, top); err != nil {
		return nil, nil, err
	}

	// Let clicks pass through to whatever lies beneath.
	win.ConnectRealize(func() {
		if region, err := cairo.RegionCreate(); err == nil {
			gdk.BaseSurface(win.Surface()).SetInputRegion(region)
		}
	})
	return win, pic, nil
}

// monitorAt returns the monitor containing the centre of the bubble and the
// bubble's offset from that monitor's top-left corner.
func monitorAt(display *gdk.Display, x, y, diameter int) (*gdk.Monitor, int, int) {
	cx, cy := x+diameter/2, y+diameter/2
	monitors := display.Monitors()
	for i := range monitors.NItems() {
		m, ok := monitors.Item(i).Cast().(*gdk.Monitor)
		if !ok {
			continue
		}
		g := m.Geometry()
		if cx >= g.X() && cx < g.X()+g.Width() && cy >= g.Y() && cy < g.Y()+g.Height() {
			return m, x - g.X(), y - g.Y()
		}
	}
	return nil, x, y
}
