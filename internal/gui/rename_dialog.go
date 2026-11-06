//go:build !nogui

package gui

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// nameFormOpts describes the name being asked for.
type nameFormOpts struct {
	Mode   NameMode
	Action NameAction
	// Dir and Ext locate the file; Ext is fixed and shown as a suffix.
	Dir, Ext string
	// Current is the file being renamed in ModeRename.
	Current string
	// Stem is the name offered to start with.
	Stem string
	// AllowOverwrite lets an existing file be chosen on purpose.
	AllowOverwrite bool
}

// nameForm is the name entry shared by the in-app rename dialogue and the
// standalone entry window: a stem field with its fixed extension and a line
// that explains whatever is wrong with the name.
type nameForm struct {
	box      *gtk.Box
	entry    *gtk.Entry
	status   *gtk.Label
	spinner  *gtk.Spinner
	revealer *gtk.Revealer
	opts     nameFormOpts
	onChange func(NameCheck, Presentation)

	problem string
	hint    string
	// programmatic marks a change made by the program rather than the user.
	programmatic bool
	typed        bool
}

func newNameForm(o nameFormOpts, onChange func(NameCheck, Presentation)) *nameForm {
	f := &nameForm{opts: o, onChange: onChange}

	f.entry = gtk.NewEntry()
	f.entry.SetHExpand(true)
	f.entry.SetActivatesDefault(true)
	f.entry.SetText(o.Stem)
	f.entry.SetInputPurpose(gtk.InputPurposeFreeForm)

	suffix := gtk.NewButtonWithLabel(o.Ext)
	suffix.SetSensitive(false)
	suffix.SetFocusable(false)
	suffix.AddCSSClass("monospace")

	linked := gtk.NewBox(gtk.OrientationHorizontal, 0)
	linked.AddCSSClass("linked")
	linked.Append(f.entry)
	linked.Append(suffix)

	f.spinner = gtk.NewSpinner()
	f.spinner.SetVisible(false)
	f.status = label("", "caption")
	f.status.SetWrap(true)
	f.status.SetXAlign(0)
	f.status.SetHExpand(true)
	row := gtk.NewBox(gtk.OrientationHorizontal, 6)
	row.SetMarginTop(6)
	row.Append(f.spinner)
	row.Append(f.status)
	f.revealer = gtk.NewRevealer()
	f.revealer.SetTransitionType(gtk.RevealerTransitionTypeSlideDown)
	f.revealer.SetChild(row)

	f.box = gtk.NewBox(gtk.OrientationVertical, 0)
	f.box.Append(linked)
	f.box.Append(f.revealer)

	f.entry.ConnectChanged(func() {
		if !f.programmatic {
			f.typed = true
			f.problem = ""
		}
		f.refresh()
	})
	f.entry.ConnectMap(func() {
		f.entry.GrabFocus()
		f.entry.SelectRegion(0, -1)
	})
	return f
}

// check classifies the name as it stands.
func (f *nameForm) check() NameCheck {
	o := f.opts
	return CheckName(o.Mode, o.Dir, o.Ext, o.Current, f.entry.Text())
}

// refresh shows the state of the name and tells the owner about it.
func (f *nameForm) refresh() {
	c := f.check()
	p := Present(f.opts.Action, c, f.opts.AllowOverwrite)

	for _, class := range []string{"error", "warning", "dim-label"} {
		f.status.RemoveCSSClass(class)
	}
	msg, busy := p.Message, false
	switch {
	case msg != "" && c.State == NameInvalid:
		f.status.AddCSSClass("error")
	case msg != "":
		f.status.AddCSSClass("warning")
	case f.problem != "":
		msg = f.problem
		f.status.AddCSSClass("warning")
	case f.hint != "":
		msg, busy = f.hint, true
		f.status.AddCSSClass("dim-label")
	}
	f.status.SetText(msg)
	f.spinner.SetVisible(busy)
	f.spinner.SetSpinning(busy)
	f.revealer.SetRevealChild(msg != "")

	if f.onChange != nil {
		f.onChange(c, p)
	}
}

// setProblem explains why the name was asked for again; it stays until the
// user edits the field.
func (f *nameForm) setProblem(msg string) {
	f.problem = msg
	f.refresh()
}

// setHint shows progress text, with a spinner, until it is cleared.
func (f *nameForm) setHint(msg string) {
	f.hint = msg
	f.refresh()
}

// suggest offers stem unless the user has already begun typing.
func (f *nameForm) suggest(stem string) {
	if f.typed {
		return
	}
	f.programmatic = true
	f.entry.SetText(stem)
	f.programmatic = false
	f.entry.SelectRegion(0, -1)
}
