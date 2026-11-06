package gui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// NameMode says what the name being chosen is for.
type NameMode int

const (
	// ModeRename renames an existing file; keeping its name is not a collision.
	ModeRename NameMode = iota
	// ModeNewFile names a file about to be created, so any existing file in
	// the way is a collision, the suggested name included.
	ModeNewFile
)

// NameState classifies a typed file name: nothing typed, unsuitable,
// suitable but already in use, or suitable and available.
type NameState int

// The states a typed name can be in.
const (
	NameEmpty NameState = iota
	NameInvalid
	NameTaken
	NameFree
)

// NameCheck is the verdict on a typed name. Target is empty unless the name
// is suitable, that is NameTaken or NameFree.
type NameCheck struct {
	State  NameState
	Target string
}

// stemOf trims the name and drops a typed extension, which is always fixed.
func stemOf(name, ext string) string {
	name = strings.TrimSpace(name)
	if ext != "" {
		name = strings.TrimSpace(strings.TrimSuffix(name, ext))
	}
	return name
}

// CheckName classifies name for a file in dir carrying the fixed extension
// ext. current is the existing file in ModeRename and is ignored otherwise.
func CheckName(mode NameMode, dir, ext, current, name string) NameCheck {
	stem := stemOf(name, ext)
	switch {
	case stem == "":
		return NameCheck{State: NameEmpty}
	case stem == "." || stem == ".." || strings.ContainsAny(stem, "/\x00"):
		return NameCheck{State: NameInvalid}
	}
	target := filepath.Join(dir, stem+ext)
	if mode == ModeRename && target == current {
		return NameCheck{State: NameFree, Target: target}
	}
	if _, err := os.Lstat(target); err == nil {
		return NameCheck{State: NameTaken, Target: target}
	}
	return NameCheck{State: NameFree, Target: target}
}

// RenameTarget validates a new base name and returns the resulting path,
// always carrying the original extension and never an existing file.
func RenameTarget(oldPath, name string) (string, error) {
	c := CheckName(ModeRename, filepath.Dir(oldPath), filepath.Ext(oldPath), oldPath, name)
	switch c.State {
	case NameEmpty, NameInvalid:
		return "", errors.New("that is not a suitable file name")
	case NameTaken:
		return "", fmt.Errorf("%s already exists", filepath.Base(c.Target))
	}
	return c.Target, nil
}

// NameAction is what the dialogue is asked for: renaming a file, saving a
// new one, or editing one before it is saved.
type NameAction string

// The actions a name can be asked for.
const (
	ActionRename NameAction = "rename"
	ActionSave   NameAction = "save"
	ActionEdit   NameAction = "edit"
)

// Valid reports whether a is a known action.
func (a NameAction) Valid() bool {
	return a == ActionRename || a == ActionSave || a == ActionEdit
}

// Verb is the label of the confirming button.
func (a NameAction) Verb() string {
	switch a {
	case ActionSave:
		return "Save"
	case ActionEdit:
		return "Edit"
	}
	return "Rename"
}

// NounFor names the kind of capture a file with extension ext holds.
func NounFor(ext string) string {
	if strings.EqualFold(ext, ".png") {
		return "screenshot"
	}
	return "recording"
}

// Title is the dialogue heading.
func (a NameAction) Title(noun string) string {
	return a.Verb() + " " + noun
}

// Presentation says how the dialogue shows a NameCheck.
type Presentation struct {
	// Label is the confirming button's label.
	Label string
	// Enabled reports whether the name may be accepted.
	Enabled bool
	// Destructive marks an acceptance that replaces an existing file.
	Destructive bool
	// Message explains the state; it is empty when nothing needs saying.
	Message string
}

// Present turns a check into what the dialogue shows. Overwriting is
// offered only when allowOverwrite is set.
func Present(action NameAction, c NameCheck, allowOverwrite bool) Presentation {
	p := Presentation{Label: action.Verb()}
	switch c.State {
	case NameInvalid:
		p.Message = "That is not a suitable file name."
	case NameTaken:
		p.Message = filepath.Base(c.Target) + " already exists in this folder."
		if allowOverwrite {
			p.Label, p.Enabled, p.Destructive = "Overwrite", true, true
		}
	case NameFree:
		p.Enabled = true
	}
	return p
}

// CommitFile moves src to target. Unless overwrite is set it never replaces
// an existing path, failing with an error satisfying os.ErrExist instead.
func CommitFile(src, target string, overwrite bool) error {
	if overwrite {
		return os.Rename(src, target)
	}
	err := unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.ENOTSUP) {
		// The filesystem has no atomic no-replace rename; a hard link
		// fails on an existing name just the same.
		if err = os.Link(src, target); err == nil {
			return os.Remove(src)
		}
	}
	return err
}

// WriteNewFile saves data as target through a temporary file in the same
// folder, so an existing file is never truncated.
func WriteNewFile(target string, data []byte, overwrite bool) error {
	f, err := os.CreateTemp(filepath.Dir(target), ".tequilashot-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return CommitFile(tmp, target, overwrite)
}
