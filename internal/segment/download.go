//go:build !nogui

package segment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

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

// rejectInsecureRedirect keeps the download on HTTPS when a mirror redirects.
func rejectInsecureRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refusing to follow a redirect to %s", req.URL)
	}
	return nil
}

// downloadClient fetches the model; tests replace it to trust their own server.
var downloadClient = &http.Client{CheckRedirect: rejectInsecureRedirect}

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
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ModelURL(), http.NoBody)
	if err != nil {
		return err
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".model-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	defer func() { _ = tmp.Close() }()

	total := resp.ContentLength
	if total <= 0 {
		total = ModelSize
	}
	hash := sha256.New()
	buf := make([]byte, 64<<10)
	var done int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := tmp.Write(buf[:n]); err != nil {
				return err
			}
			hash.Write(buf[:n])
			done += int64(n)
			if done > maxModelBytes {
				return fmt.Errorf("download failed: the file is larger than the expected model")
			}
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("download failed: %w", rerr)
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != modelSHA256 {
		return fmt.Errorf("the downloaded model is corrupt")
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}
