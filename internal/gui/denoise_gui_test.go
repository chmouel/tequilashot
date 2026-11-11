//go:build !nogui

package gui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/tequilashot/internal/prefs"
)

// autoDenoise must leave the recording as saved when the model is missing.
func TestAutoDenoiseKeepsRecordingWithoutModel(t *testing.T) {
	a := trimGUIApp(t)
	t.Setenv(prefs.EnvDenoiseModel, filepath.Join(t.TempDir(), "missing.rnnn"))
	restore := prefs.Current()
	t.Cleanup(func() { prefs.Set(restore) })
	p := restore.Clone()
	p.AutoDenoise = true
	prefs.Set(p)

	a.showResult(filepath.Join(t.TempDir(), "clip.mp4"), MediaInfo{Duration: time.Second}, ModeVideo)
	a.autoDenoise()
	if a.res.cleaning != nil {
		t.Fatal("cleaning started without a model")
	}
}

// The preference must not touch screenshots, which have no sound.
func TestAutoDenoiseLeavesScreenshotsAlone(t *testing.T) {
	a := trimGUIApp(t)
	t.Setenv(prefs.EnvDenoiseModel, filepath.Join(t.TempDir(), "missing.rnnn"))
	restore := prefs.Current()
	t.Cleanup(func() { prefs.Set(restore) })
	p := restore.Clone()
	p.AutoDenoise = true
	prefs.Set(p)

	a.showResult(filepath.Join(t.TempDir(), "shot.png"), MediaInfo{}, ModeScreenshot)
	a.autoDenoise()
	if a.res.cleaning != nil {
		t.Fatal("a screenshot was sent to noise removal")
	}
}
