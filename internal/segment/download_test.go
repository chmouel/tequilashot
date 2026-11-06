//go:build !nogui

package segment

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/tequilashot/internal/prefs"
)

func TestDownloadModelStopsOversizedMirror(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "model.onnx")
	t.Setenv(ModelEnv, "")
	old := prefs.Current()
	p := prefs.Default()
	p.ModelMirror = srv.URL
	p.SegmentationModel = dest
	prefs.Set(p)
	oldClient, oldLimit := downloadClient, maxModelBytes
	downloadClient, maxModelBytes = srv.Client(), 1024
	t.Cleanup(func() {
		prefs.Set(old)
		downloadClient, maxModelBytes = oldClient, oldLimit
	})

	err := DownloadModel(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("err = %v, want size limit", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("model left behind: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(dest))
	if len(entries) != 0 {
		t.Fatalf("temporary files left: %v", entries)
	}
}

func TestDownloadModelRejectsInsecureRedirect(t *testing.T) {
	var plainHits int
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		plainHits++
		_, _ = w.Write([]byte("x"))
	}))
	defer plain.Close()

	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL, http.StatusFound)
	}))
	defer secure.Close()

	dest := filepath.Join(t.TempDir(), "model.onnx")
	t.Setenv(ModelEnv, "")
	old := prefs.Current()
	p := prefs.Default()
	p.ModelMirror = secure.URL
	p.SegmentationModel = dest
	prefs.Set(p)
	oldClient := downloadClient
	downloadClient = &http.Client{
		Transport:     secure.Client().Transport,
		CheckRedirect: rejectInsecureRedirect,
	}
	t.Cleanup(func() {
		prefs.Set(old)
		downloadClient = oldClient
	})

	err := DownloadModel(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err = %v, want insecure redirect refusal", err)
	}
	if plainHits != 0 {
		t.Fatalf("plain HTTP server was reached %d times", plainHits)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("model left behind: %v", err)
	}
}
