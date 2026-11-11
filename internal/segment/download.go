//go:build !nogui

package segment

import (
	"context"
	"net/http"

	"github.com/chmouel/tequilashot/internal/fetch"
	"github.com/chmouel/tequilashot/internal/onnx"
	"github.com/chmouel/tequilashot/internal/prefs"
)

const (
	modnetModelURL    = "https://huggingface.co/Xenova/modnet/resolve/fa2fa546052fba4c08921230a26cc69a333fca12/onnx/model.onnx"
	modnetModelSHA256 = "07c308cf0fc7e6e8b2065a12ed7fc07e1de8febb7dc7839d7b7f15dd66584df9"
)

// maxModelBytes stops a download that grows beyond the expected model, so a
// faulty mirror cannot fill the disc.
var maxModelBytes int64 = 200_000_000

// downloadClient fetches the model; tests replace it to trust their own server.
var downloadClient = &http.Client{CheckRedirect: fetch.RejectInsecureRedirect}

// ModelURL returns the address the MODNet model is downloaded from: the
// preferred mirror or upstream. The checksum is verified either way.
func ModelURL() string {
	if m := prefs.Current().ModelMirror; m != "" {
		return m
	}
	return modnetModelURL
}

// RuntimeAvailable returns an error when ONNX Runtime cannot be found. The
// library comes from the system and is not downloaded.
func RuntimeAvailable() error {
	_, err := onnx.LibraryPath()
	return err
}

// DownloadModel fetches the MODNet model into ModnetModelPath, reporting bytes
// received and the expected total. The file is verified before it replaces any
// existing one, so an interrupted or corrupt download leaves nothing behind.
func DownloadModel(ctx context.Context, progress func(done, total int64)) error {
	return DownloadModelTo(ctx, ModnetModelPath(), progress)
}

// DownloadModelTo is DownloadModel with an explicit destination.
func DownloadModelTo(ctx context.Context, dest string, progress func(done, total int64)) error {
	return fetch.Verified(ctx, downloadClient, fetch.File{
		URL: ModelURL(), SHA256: modnetModelSHA256, Size: ModnetModelSize, MaxBytes: maxModelBytes,
	}, dest, progress)
}
