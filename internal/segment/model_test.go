//go:build !nogui

package segment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modelFiles names both model paths in a temporary directory through the
// environment.
func modelFiles(t *testing.T) (modnet, u2net string) {
	t.Helper()
	dir := t.TempDir()
	modnet = filepath.Join(dir, "modnet.onnx")
	u2net = filepath.Join(dir, "u2net.onnx")
	t.Setenv(ModnetModelEnv, modnet)
	t.Setenv(ModelEnv, u2net)
	return modnet, u2net
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveMattingModel(t *testing.T) {
	for _, tc := range []struct {
		name          string
		model         string
		modnet, u2net bool
		want          mattingModel
		wantU2Net     bool
	}{
		{name: "nothing installed", model: "", want: modnetModel},
		{name: "default falls back to U²-Net", model: "", u2net: true, want: u2netModel, wantU2Net: true},
		{name: "explicit MODNet does not fall back", model: "modnet", u2net: true, want: modnetModel},
		{name: "explicit U²-Net", model: "u2net", u2net: true, want: u2netModel, wantU2Net: true},
		{name: "both installed, default", modnet: true, u2net: true, want: modnetModel},
		{name: "both installed, U²-Net asked for", model: "u2net", modnet: true, u2net: true, want: u2netModel, wantU2Net: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modnet, u2net := modelFiles(t)
			if tc.modnet {
				touch(t, modnet)
			}
			if tc.u2net {
				touch(t, u2net)
			}
			kind, path, err := resolveMattingModel(tc.model)
			if err != nil {
				t.Fatalf("resolveMattingModel(%q): %v", tc.model, err)
			}
			if kind != tc.want {
				t.Fatalf("resolveMattingModel(%q) = %v, want %v", tc.model, kind, tc.want)
			}
			want := modnet
			if tc.wantU2Net {
				want = u2net
			}
			if path != want {
				t.Fatalf("resolveMattingModel(%q) path = %q, want %q", tc.model, path, want)
			}
		})
	}
}

func TestResolveMattingModelRejectsAnUnknownName(t *testing.T) {
	modelFiles(t)
	if _, _, err := resolveMattingModel("human-seg"); err == nil {
		t.Fatal("an unknown model name was accepted")
	}
}

func TestModelInstalledFollowsTheSelectedModel(t *testing.T) {
	modnet, u2net := modelFiles(t)
	t.Setenv(MattingModelEnv, "")
	if ModelInstalled() {
		t.Fatal("no model file, but ModelInstalled reported one")
	}
	touch(t, u2net)
	if !ModelInstalled() {
		t.Fatal("the U²-Net fallback is in place, but ModelInstalled said otherwise")
	}
	t.Setenv(MattingModelEnv, "modnet")
	if ModelInstalled() {
		t.Fatal("an explicitly requested MODNet must not fall back to U²-Net")
	}
	touch(t, modnet)
	if !ModelInstalled() {
		t.Fatal("the requested MODNet file is in place")
	}
}

// resolveFor is what New and NewWithModel do, without loading the model.
func resolveFor(name string) (mattingModel, string, error) {
	return resolveMattingModel(mattingChoice(name))
}

func TestMattingChoicePrecedence(t *testing.T) {
	t.Setenv(MattingModelEnv, "u2net")
	if got := mattingChoice("modnet"); got != "modnet" {
		t.Fatalf("a named model must win over the environment, got %q", got)
	}
	t.Setenv(MattingModelEnv, "")
	if got := mattingChoice(""); got != "" {
		t.Fatalf("an unnamed model with no environment = %q, want the empty default", got)
	}
}

func TestResolveHonoursTheEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name          string
		env           string
		modnet, u2net bool
		want          mattingModel
		wantErr       bool
	}{
		{name: "environment chooses U²-Net", env: "u2net", u2net: true, want: u2netModel},
		{name: "explicit environment MODNet never falls back", env: "modnet", u2net: true, want: modnetModel},
		{name: "environment MODNet in place", env: "modnet", modnet: true, want: modnetModel},
		{name: "unset environment defaults to MODNet", env: "", u2net: true, want: u2netModel},
		{name: "invalid environment is refused", env: "u2netp", modnet: true, u2net: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modnet, u2net := modelFiles(t)
			if tc.modnet {
				touch(t, modnet)
			}
			if tc.u2net {
				touch(t, u2net)
			}
			t.Setenv(MattingModelEnv, tc.env)
			kind, _, err := resolveFor("")
			if tc.wantErr {
				if err == nil {
					t.Fatal("an invalid environment value was accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveFor(%q): %v", tc.env, err)
			}
			if kind != tc.want {
				t.Fatalf("environment %q resolved to %v, want %v", tc.env, kind, tc.want)
			}
		})
	}
}

func TestNamedModelWinsOverTheEnvironment(t *testing.T) {
	modnet, u2net := modelFiles(t)
	touch(t, modnet)
	touch(t, u2net)
	t.Setenv(MattingModelEnv, "modnet")
	kind, path, err := resolveFor("u2net")
	if err != nil {
		t.Fatal(err)
	}
	if kind != u2netModel || path != u2net {
		t.Fatalf("the named model resolved to %v at %q, want U²-Net at %q", kind, path, u2net)
	}
}

func TestResolveReportsAnUnreadableModel(t *testing.T) {
	modnet, u2net := modelFiles(t)
	touch(t, u2net)
	if err := os.Symlink(modnet, modnet); err != nil {
		t.Skipf("cannot create a symlink loop: %v", err)
	}
	t.Setenv(MattingModelEnv, "")
	if _, _, err := resolveFor(""); err == nil {
		t.Fatal("an unreadable MODNet file was treated as missing and fell back")
	}
}

func TestNeedsDownloadAndModelProblem(t *testing.T) {
	modnet, u2net := modelFiles(t)
	t.Setenv(MattingModelEnv, "")
	if !NeedsDownload() {
		t.Fatal("a missing MODNet file should be downloadable")
	}
	if err := ModelProblem(); err == nil || !strings.Contains(err.Error(), "MODNet model not found") {
		t.Fatalf("ModelProblem() = %v, want the missing MODNet model", err)
	}

	touch(t, u2net)
	if ModelInstalled() != true || NeedsDownload() || ModelProblem() != nil {
		t.Fatal("the U²-Net fallback should make matting ready without a download")
	}

	if err := os.Remove(u2net); err != nil {
		t.Fatal(err)
	}
	t.Setenv(MattingModelEnv, "u2net")
	if NeedsDownload() {
		t.Fatal("downloading MODNet must not be offered for a missing, explicitly chosen U²-Net")
	}
	if err := ModelProblem(); err == nil || !strings.Contains(err.Error(), "U²-Net model not found") {
		t.Fatalf("ModelProblem() = %v, want the missing U²-Net model", err)
	}

	t.Setenv(MattingModelEnv, "u2netp")
	if NeedsDownload() || ModelProblem() == nil {
		t.Fatal("an invalid environment value must be reported and not downloaded")
	}

	t.Setenv(MattingModelEnv, "")
	touch(t, modnet)
	if NeedsDownload() || ModelProblem() != nil {
		t.Fatal("MODNet in place should need nothing")
	}
}

func TestNewReadsTheEnvironment(t *testing.T) {
	modelFiles(t)
	t.Setenv(MattingModelEnv, "u2netp")
	for _, load := range []struct {
		name string
		call func() error
	}{
		{"New", func() error { _, err := New(); return err }},
		{"NewWithModel", func() error { _, err := NewWithModel(""); return err }},
	} {
		if err := load.call(); err == nil || !strings.Contains(err.Error(), "unknown matting model") {
			t.Fatalf("%s() = %v, want the invalid environment value to be refused", load.name, err)
		}
	}
	err := func() error { _, err := NewWithModel("u2net"); return err }()
	if err == nil || !strings.Contains(err.Error(), "U²-Net model not found") {
		t.Fatalf("a named model over an invalid environment = %v, want the missing U²-Net file", err)
	}
}
