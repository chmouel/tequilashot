//go:build !nogui

package gui

import (
	"errors"
	"image"
	"sync"
	"sync/atomic"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/chmouel/tequilashot/internal/segment"
)

// matteStall is how long one frame may take before the segmentation counts
// as failed.
const matteStall = 5 * time.Second

// cutter removes the background from camera frames off the GTK main thread.
// It keeps one frame being processed and the newest one waiting, and each
// result belongs to the frame it was computed from. A non-nil bg replaces the
// background instead of leaving it transparent; flipBg mirrors it first, for a
// picture that is mirrored again on screen.
type cutter struct {
	mu       sync.Mutex
	closed   bool
	in       chan []byte
	done     chan struct{}
	busy     atomic.Int64 // unix nanoseconds when the current frame began, or zero
	failOnce sync.Once
	onFrame  func(*image.RGBA)
	onFail   func(error)
	bg       image.Image // replaces the background when not nil
	flipBg   bool
	model    string // empty follows the environment
}

// newCutter starts the worker for the model named by model, or the one the
// environment names when that is empty. onFrame and onFail run on the worker
// goroutine and onFail is called at most once; no frame is delivered after it.
func newCutter(model string, bg image.Image, flipBg bool, onFrame func(*image.RGBA), onFail func(error)) *cutter {
	c := &cutter{in: make(chan []byte, 1), done: make(chan struct{}), onFrame: onFrame, onFail: onFail, bg: bg, flipBg: flipBg, model: model}
	go c.run()
	go c.watch()
	return c
}

func (c *cutter) fail(err error) { c.failOnce.Do(func() { c.onFail(err) }) }

// Put queues a frame, dropping an older one that has not started.
func (c *cutter) Put(frame []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for !c.closed {
		select {
		case c.in <- frame:
			return
		default:
		}
		select {
		case <-c.in:
		default:
		}
	}
}

func (c *cutter) run() {
	defer close(c.done)
	m, err := segment.NewWithModel(c.model)
	if err != nil {
		c.fail(err)
		return
	}
	defer m.Close()
	m.SetBackground(c.bg, c.flipBg)
	for frame := range c.in {
		c.busy.Store(time.Now().UnixNano())
		img, err := m.Cut(frame)
		c.busy.Store(0)
		if err != nil {
			c.fail(err)
			return
		}
		c.onFrame(img)
	}
}

func (c *cutter) watch() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-tick.C:
			if t := c.busy.Load(); t != 0 && time.Since(time.Unix(0, t)) > matteStall {
				c.fail(errors.New("the background removal stopped responding"))
				return
			}
		}
	}
}

// Close stops the worker once the frame in hand is done.
func (c *cutter) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.in)
	}
}

// rgbaTexture wraps a premultiplied frame in a texture.
func rgbaTexture(img *image.RGBA) *gdk.MemoryTexture {
	return gdk.NewMemoryTexture(img.Rect.Dx(), img.Rect.Dy(), gdk.MemoryR8G8B8A8Premultiplied,
		glib.NewBytes(img.Pix), uint(img.Stride))
}
