//go:build !nogui

package gui

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"log"
	"math"
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
	Area     image.Rectangle
	Size     string
	Width    int // capture mode
	Height   int
	FPS      int
	Mirror   bool
	// RemoveBackground shows only the person, with the rest transparent.
	RemoveBackground bool
	NoRing           bool
}

// bubbleCSS draws a transparent window holding a circular picture with a ring.
const bubbleCSS = `
window.easyshot-bubble, window.easyshot-bubble.background { background: transparent; box-shadow: none; }
.easyshot-bubble-disc {
	border-radius: 9999px;
	border: %dpx solid rgba(255, 255, 255, 0.92);
	background: #1e1e1e;
}
window.easyshot-bubble .easyshot-bubble-disc.cutout { background: transparent; }
window.easyshot-bubble .easyshot-bubble-disc.noring { border-color: transparent; }
.easyshot-bubble-disc picture.mirrored { transform: scale(-1, 1); }
`

// RunCameraBubble shows the camera as a draggable circle until it is sent
// SIGTERM or SIGINT. It prints "ready" on stdout once the first picture is on
// screen, and fails if the camera cannot be opened.
func RunCameraBubble(opts BubbleOptions) error {
	// The recorder preloads gtk4-layer-shell for us; ffmpeg must not inherit it.
	_ = os.Unsetenv("LD_PRELOAD")
	if !external.ValidCameraDevice(opts.Device) || opts.Diameter <= 0 || opts.Area.Empty() {
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
	var cut *cutter
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
		win, pic, cycle, err := buildBubble(app, opts)
		if err != nil {
			fail(err)
			return
		}
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			for scanner.Scan() {
				if scanner.Text() == "cycle" {
					glib.IdleAdd(func() {
						position, err := cycle()
						if err != nil {
							fmt.Println(err)
						} else {
							fmt.Println(position)
						}
					})
				}
			}
		}()

		ready := false
		show := func(tex gdk.Paintabler) {
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
		}

		// With the background removed, raw frames never reach the screen: the
		// window stays hidden until the first cut-out picture is ready, and
		// hides again if the cutting fails.
		var onFrame func([]byte)
		if opts.RemoveBackground {
			cut = newCutter(
				func(img *image.RGBA) {
					glib.IdleAdd(func() {
						if failure == nil {
							show(rgbaTexture(img))
						}
					})
				},
				func(err error) {
					glib.IdleAdd(func() {
						pic.SetPaintable(nil)
						win.SetVisible(false)
						fail(err)
					})
				},
			)
			onFrame = cut.Put
		} else {
			latest := &external.LatestFrame{}
			onFrame = func(frame []byte) {
				if !latest.Put(frame) {
					return
				}
				glib.IdleAdd(func() {
					data := latest.Take()
					if data == nil {
						return
					}
					if tex, err := gdk.NewTextureFromBytes(glib.NewBytes(data)); err == nil {
						show(tex)
					}
				})
			}
		}
		preview = external.StartPreview(&gate, opts.Device, opts.Width, opts.Height, opts.FPS, onFrame,
			func() {
				glib.IdleAdd(func() { fail(fmt.Errorf("camera %s could not be opened", opts.Device)) })
			})
	})

	code := app.Run([]string{os.Args[0]})
	gate.Retire(preview)
	if cut != nil {
		cut.Close()
	}
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
func buildBubble(app *gtk.Application, opts BubbleOptions) (*gtk.Window, *gtk.Picture, func() (string, error), error) {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return nil, nil, nil, errors.New("no display available")
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
	if opts.RemoveBackground {
		disc.AddCSSClass("cutout")
		if opts.NoRing {
			disc.AddCSSClass("noring")
		}
	}
	disc.SetOverflow(gtk.OverflowHidden)
	disc.SetSizeRequest(opts.Diameter, opts.Diameter)
	pic.SetVExpand(true)
	disc.Append(pic)

	// The window covers the whole monitor and the disc moves within it, so a
	// drag never waits on the compositor to move the surface.
	monitor, left, top := monitorAt(display, opts.X, opts.Y, opts.Diameter)
	origin := image.Pt(opts.X-left, opts.Y-top)
	fixed := gtk.NewFixed()
	fixed.Put(disc, float64(left), float64(top))

	win := gtk.NewWindow()
	win.SetApplication(app)
	win.AddCSSClass("easyshot-bubble")
	win.SetDecorated(false)
	win.SetChild(fixed)
	if err := placeLayer(win, monitor); err != nil {
		return nil, nil, nil, err
	}

	// Only the disc takes the pointer; clicks elsewhere reach what lies beneath.
	setInput := func() {
		if surface := win.Surface(); surface != nil {
			if region, err := circleRegion(left, top, opts.Diameter); err == nil {
				gdk.BaseSurface(surface).SetInputRegion(region)
			}
		}
	}
	win.ConnectRealize(setInput)
	move := func(x, y int) {
		left, top = x, y
		fixed.Move(disc, float64(left), float64(top))
	}
	dragging := false
	cycle := func() (string, error) {
		if dragging {
			return "", errors.New("finish dragging the round camera first")
		}
		area := opts.Area.Sub(origin)
		position, corner, err := nextBubbleCorner(area, image.Rect(0, 0, fixed.Width(), fixed.Height()), opts.Diameter, opts.Size, image.Pt(left, top))
		if err != nil {
			return "", err
		}
		move(position.X, position.Y)
		setInput()
		return corner, nil
	}

	disc.SetCursorFromName("grab")
	drag := gtk.NewGestureDrag()
	startX, startY := left, top
	drag.ConnectDragBegin(func(_, _ float64) {
		dragging = true
		startX, startY = left, top
		disc.SetCursorFromName("grabbing")
	})
	drag.ConnectDragUpdate(func(dx, dy float64) {
		move(clampInt(startX+int(dx), 0, fixed.Width()-opts.Diameter),
			clampInt(startY+int(dy), 0, fixed.Height()-opts.Diameter))
	})
	drag.ConnectDragEnd(func(_, _ float64) {
		dragging = false
		disc.SetCursorFromName("grab")
		setInput()
	})
	disc.AddController(drag)
	return win, pic, cycle, nil
}

// circleRegion approximates a disc of diameter d at x, y with one-pixel strips.
func circleRegion(x, y, d int) (*cairo.Region, error) {
	r := float64(d) / 2
	strips := make([]*cairo.Rectangle, 0, d)
	for row := range d {
		dy := float64(row) + 0.5 - r
		half := int(math.Round(math.Sqrt(max(0, r*r-dy*dy))))
		if half > 0 {
			strips = append(strips, cairo.RectangleNew(x+int(r)-half, y+row, 2*half, 1))
		}
	}
	if len(strips) == 0 {
		return cairo.RegionCreate()
	}
	var region *cairo.Region
	return region.CreateRectangles(strips...)
}

func clampInt(v, lo, hi int) int {
	return max(lo, min(v, hi))
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
