package prefs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingAndSanitise(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences.json")
	p, err := Load(path)
	if err != nil || !reflect.DeepEqual(p, Default()) {
		t.Fatalf("missing file: got %+v, %v", p, err)
	}

	bad := `{"frame_rate": 7, "cleanup_days": 0, "replay_bitrate_kbps": 5, "video_codec": "vp9",
		"recording_args": "-o /tmp/x", "selection_colour": "red", "model_mirror": "http://example.com/m",
		"programs": {"rm": "/bin/rm", "grim": " ", "slurp": "/opt/slurp"}, "max_width": 2560}`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.MaxWidth = 2560
	want.ProgramPaths = map[string]string{"slurp": "/opt/slurp"}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("sanitised = %+v, want %+v", p, want)
	}

	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "preferences.json")
	p := Default()
	p.SaveLocation = "/srv/caps"
	p.RecordingArgs = "-ac opus"
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
}

func TestCleanupActive(t *testing.T) {
	t.Setenv(EnvSaveLocation, "")
	legacy := LegacySaveDir()
	tests := []struct {
		name string
		p    func(*Prefs)
		dir  string
		want bool
	}{
		{"untouched legacy folder", func(*Prefs) {}, legacy, true},
		{"untouched other folder", func(*Prefs) {}, "/srv/other", false},
		{"switched off", func(p *Prefs) { p.CleanupEnabled = false }, legacy, false},
		{"folder changed without consent", func(p *Prefs) { p.SaveLocationChanged = true }, legacy, false},
		{"consent for folder", func(p *Prefs) {
			p.SaveLocationChanged, p.CleanupFolder = true, "/srv/caps/"
		}, "/srv/caps", true},
		{"consent for another folder", func(p *Prefs) {
			p.SaveLocationChanged, p.CleanupFolder = true, "/srv/caps"
		}, "/srv/other", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Default()
			tt.p(&p)
			if got := p.CleanupActive(tt.dir); got != tt.want {
				t.Fatalf("CleanupActive(%q) = %v, want %v", tt.dir, got, tt.want)
			}
		})
	}
}

func TestSaveDirPrecedence(t *testing.T) {
	p := Default()
	p.SaveLocation = "/srv/prefs"
	t.Setenv(EnvSaveLocation, "/srv/env")
	if got := p.SaveDir(); got != "/srv/env" {
		t.Fatalf("env should win, got %q", got)
	}
	t.Setenv(EnvSaveLocation, "")
	if got := p.SaveDir(); got != "/srv/prefs" {
		t.Fatalf("prefs should win over default, got %q", got)
	}
}

func TestReloadKeepsPreviousOnMalformedFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	off := Default()
	off.CleanupEnabled = false
	if err := Save(Path(), off); err != nil {
		t.Fatal(err)
	}
	if err := Reload(); err != nil || Current().CleanupEnabled {
		t.Fatalf("valid file: err=%v cleanup=%v", err, Current().CleanupEnabled)
	}
	if err := os.WriteFile(Path(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Reload(); err == nil {
		t.Fatal("malformed file: want an error so callers can skip clean-up")
	}
	if Current().CleanupEnabled {
		t.Fatal("malformed file must not bring back the default clean-up")
	}
	Set(Default())
}
