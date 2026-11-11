//go:build !nogui

package segment

import (
	"context"
	"net/http"
	"os"

	"github.com/chmouel/tequilashot/internal/fetch"
	"github.com/chmouel/tequilashot/internal/prefs"
)

const (
	modelURL    = "https://github.com/danielgatis/rembg/releases/download/v0.0.0/u2net_human_seg.onnx"
	modelSHA256 = "01eb6a29a5c4d8edb30b56adad9bb3a2a0535338e480724a213e0acfd2d1c73c"

	// ModelSize is the approximate download size in bytes.
	ModelSize = 176_000_000
)

// maxModelBytes stops a download that grows beyond the expected model, so a
// faulty mirror cannot fill the disc.
var maxModelBytes int64 = 200_000_000

// downloadClient fetches the model; tests replace it to trust their own server.
var downloadClient = &http.Client{CheckRedirect: fetch.RejectInsecureRedirect}

// ModelURL returns the address the model is downloaded from: the preferred
// mirror or the upstream release. The checksum is verified either way.
func ModelURL() string {
	if m := prefs.Current().ModelMirror; m != "" {
		return m
	}
	return modelURL
}

// ModelInstalled reports whether the model file is in place.
func ModelInstalled() bool {
	_, err := os.Stat(ModelPath())
	return err == nil
}

// RuntimeAvailable returns an error when ONNX Runtime cannot be found. The
// library comes from the system and is not downloaded.
func RuntimeAvailable() error {
	_, err := libraryPath()
	return err
}

// DownloadModel fetches the model into ModelPath, reporting bytes received and
// the expected total. The file is verified before it replaces any existing
// one, so an interrupted or corrupt download leaves nothing behind.
func DownloadModel(ctx context.Context, progress func(done, total int64)) error {
	return DownloadModelTo(ctx, ModelPath(), progress)
}

// DownloadModelTo is DownloadModel with an explicit destination.
func DownloadModelTo(ctx context.Context, dest string, progress func(done, total int64)) error {
	return fetch.Verified(ctx, downloadClient, fetch.File{
		URL: ModelURL(), SHA256: modelSHA256, Size: ModelSize, MaxBytes: maxModelBytes,
	}, dest, progress)
}
