//go:build !nogui

package gui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chmouel/tequilashot/internal/external"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

const (
	entryAppID = "com.chmouel.Tequilashot.Entry"
	// aiNameTimeout bounds the wait for a suggested name.
	aiNameTimeout = 10 * time.Second
	aiNamePrompt  = "identify a filename for that image and return only the slug of the filename, nothing else"
)

// ErrEntryCancelled reports that the name was declined or the window closed.
var ErrEntryCancelled = errors.New("naming cancelled")

// EntryOptions describes a name to ask for in the standalone window.
type EntryOptions struct {
	Action NameAction
	// Dir and Ext locate the file; Ext is fixed.
	Dir, Ext string
	// Name is the stem offered to start with.
	Name string
	// File is the image or video to preview and, when renaming, the file
	// being renamed.
	File string
	// Problem explains why the name is being asked for again.
	Problem string
	// AIModel, when set, asks aichat for a stem whilst the window is open.
	AIModel string
}

// EntryResult is an accepted name.
type EntryResult struct {
	// Name is the complete file name, extension included.
	Name string
	// Overwrite reports that replacing an existing file was chosen.
	Overwrite bool
}

// ProbeEntry reports whether the entry window can be shown here.
func ProbeEntry() bool {
	return gtk.InitCheck()
}

// RunEntry shows the naming window and blocks until it is answered.
func RunEntry(o EntryOptions) (EntryResult, error) {
	if !o.Action.Valid() {
		return EntryResult{}, errors.New("unknown naming action " + string(o.Action))
	}
	if o.Dir == "" || o.Ext == "" {
		return EntryResult{}, errors.New("a folder and an extension are required")
	}
	e := &entryWindow{o: o}
	e.app = adw.NewApplication(entryAppID, gio.ApplicationNonUnique)
	e.app.ConnectActivate(e.build)
	if code := e.app.Run([]string{os.Args[0]}); code != 0 {
		return EntryResult{}, errors.New("the naming window failed to run")
	}
	if !e.accepted {
		return EntryResult{}, ErrEntryCancelled
	}
	return e.result, nil
}

type entryWindow struct {
	o        EntryOptions
	app      *adw.Application
	win      *adw.ApplicationWindow
	form     *nameForm
	primary  *gtk.Button
	preview  *gtk.Picture
	accepted bool
	result   EntryResult
}

func (e *entryWindow) build() {
	o := e.o
	if display := gdk.DisplayGetDefault(); display != nil {
		p := gtk.NewCSSProvider()
		p.LoadFromString(css)
		gtk.StyleContextAddProviderForDisplay(display, p, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
	}

	mode := ModeNewFile
	current := ""
	if o.Action == ActionRename {
		mode, current = ModeRename, o.File
	}
	e.form = newNameForm(nameFormOpts{
		Mode: mode, Action: o.Action, Dir: o.Dir, Ext: o.Ext, Current: current,
		Stem: strings.TrimSuffix(o.Name, o.Ext), AllowOverwrite: true,
	}, e.update)

	e.primary = gtk.NewButtonWithLabel(o.Action.Verb())
	e.primary.AddCSSClass("suggested-action")
	e.primary.AddCSSClass("pill")
	e.primary.ConnectClicked(e.accept)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.AddCSSClass("pill")
	cancel.ConnectClicked(func() { e.win.Close() })

	buttons := gtk.NewBox(gtk.OrientationHorizontal, 8)
	buttons.SetHAlign(gtk.AlignEnd)
	buttons.SetMarginTop(18)
	buttons.Append(cancel)
	buttons.Append(e.primary)

	folder := label(displayDir(o.Dir), "dim-label", "caption")
	folder.SetEllipsize(pango.EllipsizeStart)
	folder.SetMarginTop(8)
	folder.SetTooltipText(o.Dir)

	field := gtk.NewBox(gtk.OrientationVertical, 6)
	field.SetHExpand(true)
	field.SetMarginTop(16)
	field.Append(label("Name", "er-section"))
	field.Append(e.form.box)
	field.Append(folder)

	body := gtk.NewBox(gtk.OrientationVertical, 0)
	if o.File != "" {
		e.preview = gtk.NewPicture()
		e.preview.AddCSSClass("er-thumb")
		e.preview.SetContentFit(gtk.ContentFitContain)
		e.preview.SetCanShrink(true)
		e.preview.SetSizeRequest(564, 300)
		e.preview.SetOverflow(gtk.OverflowHidden)
		e.preview.SetHAlign(gtk.AlignFill)
		body.Append(e.preview)
	}
	body.Append(field)

	page := gtk.NewBox(gtk.OrientationVertical, 0)
	page.SetMarginTop(6)
	page.SetMarginBottom(18)
	page.SetMarginStart(24)
	page.SetMarginEnd(24)
	page.Append(body)
	page.Append(buttons)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(adw.NewHeaderBar())
	toolbar.SetContent(page)

	e.win = adw.NewApplicationWindow(&e.app.Application)
	e.win.SetTitle(o.Action.Title(NounFor(o.Ext)))
	e.win.SetDefaultSize(612, -1)
	// A fixed-size window is floated by tiling compositors, keeping the dialogue compact.
	e.win.SetResizable(false)
	e.win.SetContent(toolbar)
	e.win.SetDefaultWidget(e.primary)

	keys := gtk.NewEventControllerKey()
	keys.ConnectKeyPressed(func(keyval, _ uint, _ gdk.ModifierType) bool {
		if keyval == gdk.KEY_Escape {
			e.win.Close()
			return true
		}
		return false
	})
	e.win.AddController(keys)

	e.form.setProblem(o.Problem)
	e.win.Present()

	if e.preview != nil {
		e.loadPreview()
	}
	if o.AIModel != "" && o.File != "" {
		e.askAI()
	}
}

// update mirrors the state of the name on the confirming button.
func (e *entryWindow) update(_ NameCheck, p Presentation) {
	e.primary.SetLabel(p.Label)
	e.primary.SetSensitive(p.Enabled)
	if p.Destructive {
		e.primary.RemoveCSSClass("suggested-action")
		e.primary.AddCSSClass("destructive-action")
	} else {
		e.primary.RemoveCSSClass("destructive-action")
		e.primary.AddCSSClass("suggested-action")
	}
}

func (e *entryWindow) accept() {
	c := e.form.check()
	if !Present(e.o.Action, c, true).Enabled {
		return
	}
	e.accepted = true
	e.result = EntryResult{Name: filepath.Base(c.Target), Overwrite: c.State == NameTaken}
	e.win.Close()
}

// loadPreview shows the capture: a PNG directly, a video as a still frame.
func (e *entryWindow) loadPreview() {
	file := e.o.File
	if e.o.Ext == ".png" {
		if tex, err := gdk.NewTextureFromFilename(file); err == nil {
			e.preview.SetPaintable(tex)
		}
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		info, err := ProbeMedia(ctx, file)
		if err != nil {
			return
		}
		data, err := ExtractFrame(ctx, file, info.Duration)
		if err != nil {
			return
		}
		glib.IdleAdd(func() {
			if tex, err := gdk.NewTextureFromBytes(glib.NewBytes(data)); err == nil {
				e.preview.SetPaintable(tex)
			}
		})
	}()
}

// askAI fetches a suggested name without holding the window back.
func (e *entryWindow) askAI() {
	e.form.setHint("Consulting the AI for a suitable name…")
	file, model, ext := e.o.File, e.o.AIModel, e.o.Ext
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), aiNameTimeout)
		defer cancel()
		out, err := external.AIChat(ctx, model, file, aiNamePrompt)
		glib.IdleAdd(func() {
			e.form.setHint("")
			if err != nil {
				return
			}
			if stem := slugStem(out, ext); stem != "" {
				e.form.suggest(stem)
			}
		})
	}()
}

// slugStem turns a model's answer into a usable stem, or "" if it has none.
func slugStem(answer, ext string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(answer), "\n")
	stem := stemOf(strings.Trim(strings.TrimSpace(line), "`'\""), ext)
	if stem == "" || stem == "." || stem == ".." {
		return ""
	}
	return strings.ReplaceAll(stem, "/", "-")
}

// displayDir shortens the home folder to a tilde.
func displayDir(dir string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, dir); err == nil && !strings.HasPrefix(rel, "..") {
			if rel == "." {
				return "~"
			}
			return "~/" + rel
		}
	}
	return dir
}
