package gui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckName(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "a.png")
	write(t, current, "a")
	write(t, filepath.Join(dir, "taken.png"), "t")
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "dangling.png")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		mode NameMode
		in   string
		want NameState
	}{
		{"empty", ModeRename, "", NameEmpty},
		{"blank", ModeNewFile, "  ", NameEmpty},
		{"only the extension", ModeNewFile, ".png", NameEmpty},
		{"dot", ModeRename, ".", NameInvalid},
		{"dotdot", ModeRename, "..", NameInvalid},
		{"slash", ModeNewFile, "x/y", NameInvalid},
		{"nul", ModeNewFile, "x\x00", NameInvalid},
		{"fresh", ModeRename, "b", NameFree},
		{"typed extension is fixed", ModeRename, "b.png", NameFree},
		{"unchanged rename", ModeRename, "a", NameFree},
		{"unchanged new file collides", ModeNewFile, "a", NameTaken},
		{"taken rename", ModeRename, "taken", NameTaken},
		{"taken new file", ModeNewFile, " taken.png ", NameTaken},
		{"dangling symlink is taken", ModeNewFile, "dangling", NameTaken},
	}
	for _, tt := range tests {
		got := CheckName(tt.mode, dir, ".png", current, tt.in)
		if got.State != tt.want {
			t.Errorf("%s: state %v, want %v", tt.name, got.State, tt.want)
		}
		if (got.State == NameFree || got.State == NameTaken) != (got.Target != "") {
			t.Errorf("%s: target %q does not match state", tt.name, got.Target)
		}
	}
}

func TestPresent(t *testing.T) {
	taken := NameCheck{State: NameTaken, Target: "/x/a.png"}
	tests := []struct {
		name  string
		check NameCheck
		allow bool
		want  Presentation
	}{
		{"empty is quiet", NameCheck{State: NameEmpty}, true, Presentation{Label: "Save"}},
		{"invalid", NameCheck{State: NameInvalid}, true, Presentation{Label: "Save", Message: "That is not a suitable file name."}},
		{"free", NameCheck{State: NameFree}, false, Presentation{Label: "Save", Enabled: true}},
		{"taken, refused", taken, false, Presentation{Label: "Save", Message: "a.png already exists in this folder."}},
		{"taken, offered", taken, true, Presentation{Label: "Overwrite", Enabled: true, Destructive: true, Message: "a.png already exists in this folder."}},
	}
	for _, tt := range tests {
		if got := Present(ActionSave, tt.check, tt.allow); got != tt.want {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
	if Present(ActionEdit, NameCheck{State: NameFree}, false).Label != "Edit" {
		t.Error("the confirming button should carry the action")
	}
}

func TestTitles(t *testing.T) {
	if got := ActionRename.Title(NounFor(".mp4")); got != "Rename recording" {
		t.Errorf("got %q", got)
	}
	if got := ActionSave.Title(NounFor(".PNG")); got != "Save screenshot" {
		t.Errorf("got %q", got)
	}
}

func TestCommitFileNeverReplacesWithoutConsent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.png")
	target := filepath.Join(dir, "target.png")
	write(t, src, "new")
	write(t, target, "old")

	// The target appeared after the name was validated.
	if err := CommitFile(src, target, false); !errors.Is(err, os.ErrExist) {
		t.Fatalf("got %v, want an existing-file error", err)
	}
	if got := read(t, target); got != "old" {
		t.Fatalf("the existing file was replaced: %q", got)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("the source must survive a refused commit")
	}

	if err := CommitFile(src, target, true); err != nil {
		t.Fatal(err)
	}
	if got := read(t, target); got != "new" {
		t.Fatalf("explicit overwrite did not replace: %q", got)
	}
}

func TestCommitFileTreatsSymlinkAsTaken(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.png")
	link := filepath.Join(dir, "link.png")
	write(t, src, "new")
	if err := os.Symlink(filepath.Join(dir, "elsewhere"), link); err != nil {
		t.Fatal(err)
	}
	if err := CommitFile(src, link, false); !errors.Is(err, os.ErrExist) {
		t.Fatalf("got %v, want an existing-file error", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "elsewhere")); err == nil {
		t.Fatal("the symlink was followed")
	}
}

func TestWriteNewFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shot.png")
	if err := WriteNewFile(target, []byte("one"), false); err != nil {
		t.Fatal(err)
	}
	if err := WriteNewFile(target, []byte("two"), false); !errors.Is(err, os.ErrExist) {
		t.Fatalf("got %v, want an existing-file error", err)
	}
	if got := read(t, target); got != "one" {
		t.Fatalf("existing file was truncated: %q", got)
	}
	if err := WriteNewFile(target, []byte("two"), true); err != nil {
		t.Fatal(err)
	}
	if got := read(t, target); got != "two" {
		t.Fatalf("got %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}
