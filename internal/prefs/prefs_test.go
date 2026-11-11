package prefs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	// An unusable period stops deletion instead of shortening the period.
	want.CleanupEnabled = false
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("sanitised = %+v, want %+v", p, want)
	}

	for _, broken := range []string{"{", "null", "[]", ""} {
		if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
			t.Fatal(err)
		}
		p, err := Load(path)
		if err == nil {
			t.Fatalf("%q: expected an error", broken)
		}
		if p.CleanupEnabled {
			t.Fatalf("%q: a damaged file must not enable deletion", broken)
		}
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

func TestLoadNullCleanupValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	legacy := filepath.Join(t.TempDir(), "Screenshots")
	for _, body := range []string{
		`{"cleanup_enabled": true, "cleanup_days": null}`,
		`{"cleanup_enabled": null, "cleanup_days": 7}`,
		`{"cleanup_enabled": true, "cleanup_days": 7, "cleanup_folder": null}`,
		`{"cleanup_enabled": true, "cleanup_days": 7, "save_location_changed": null}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		p, err := Load(path)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if p.CleanupActiveWith(legacy, legacy) {
			t.Fatalf("%s: a null clean-up setting must not leave deletion on", body)
		}
	}
}

func TestAutoDenoiseDefaultsOffAndSurvivesSave(t *testing.T) {
	if Default().AutoDenoise {
		t.Fatal("automatic noise removal must be opt-in")
	}
	path := filepath.Join(t.TempDir(), "preferences.json")
	p := Default()
	p.AutoDenoise = true
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !got.AutoDenoise {
		t.Fatalf("auto_denoise = %v, %v; want true", got.AutoDenoise, err)
	}
}

func TestAutoDenoiseEnvironmentOverride(t *testing.T) {
	restore := Current()
	t.Cleanup(func() { Set(restore) })
	p := Default()
	p.AutoDenoise = true
	Set(p)
	t.Setenv(EnvAutoDenoise, "")
	if !AutoDenoiseEnabled() {
		t.Fatal("the preference should apply without an override")
	}
	t.Setenv(EnvAutoDenoise, "off")
	if AutoDenoiseEnabled() {
		t.Fatal("the environment should be able to switch it off")
	}
	t.Setenv(EnvAutoDenoise, "perhaps")
	if !AutoDenoiseEnabled() {
		t.Fatal("an unrecognised value should leave the preference in charge")
	}
	t.Setenv(EnvAutoDenoise, "1")
	Set(Default())
	if !AutoDenoiseEnabled() {
		t.Fatal("the environment should be able to switch it on")
	}
}

func TestHideWaybarDefaultsOff(t *testing.T) {
	if Default().HideWaybar {
		t.Fatal("hiding Waybar must be opt-in")
	}
}

func TestHideWaybarSurvivesSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	p := Default()
	p.HideWaybar = true
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // a temporary file
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"hide_waybar": true`) {
		t.Fatalf("saved file lacks the hide_waybar key: %s", data)
	}
	got, err := Load(path)
	if err != nil || !got.HideWaybar {
		t.Fatalf("hide_waybar = %v, %v; want true", got.HideWaybar, err)
	}
}

func TestSanitiseKeepsHideWaybar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"hide_waybar": true, "frame_rate": 7, "video_codec": "vp9"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.HideWaybar {
		t.Error("sanitising dropped hide_waybar")
	}
	if got.FrameRate != Default().FrameRate || got.VideoCodec != "" {
		t.Errorf("invalid values were not fixed: frame_rate %d, video_codec %q", got.FrameRate, got.VideoCodec)
	}
}

func TestForceAutoDenoiseIsNotAPreference(t *testing.T) {
	t.Setenv(EnvAutoDenoise, "")
	if AutoDenoiseEnabled() {
		t.Fatal("automatic noise removal must start off")
	}
	t.Cleanup(func() { autoDenoiseSession.Store(false) })
	ForceAutoDenoise()
	if !AutoDenoiseEnabled() {
		t.Fatal("the session override should switch it on")
	}
	if Current().Clone().AutoDenoise {
		t.Fatal("the override must not enter the preferences that get saved")
	}
}

func TestModnetModelPathPrecedence(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv(EnvModnetModel, "")
	old := Current()
	t.Cleanup(func() { Set(old) })

	p := Default()
	p.ModnetModel = "/srv/prefs/modnet.onnx"
	Set(p)
	if got, want := ModnetModelPath(), "/srv/prefs/modnet.onnx"; got != want {
		t.Fatalf("prefs model path = %q, want %q", got, want)
	}
	t.Setenv(EnvModnetModel, "/srv/env/modnet.onnx")
	if got, want := ModnetModelPath(), "/srv/env/modnet.onnx"; got != want {
		t.Fatalf("env model path = %q, want %q", got, want)
	}
	t.Setenv(EnvModnetModel, "")
	Set(Default())
	if got, want := ModnetModelPath(), filepath.Join(data, "tequilashot", "modnet.onnx"); got != want {
		t.Fatalf("default model path = %q, want %q", got, want)
	}
	if got, want := DefaultSegmentationModelPath(), filepath.Join(data, "tequilashot", "u2net_human_seg.onnx"); got != want {
		t.Fatalf("default U²-Net path = %q, want %q", got, want)
	}
}
