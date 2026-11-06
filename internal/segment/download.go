//go:build !nogui

package segment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	modelURL    = "https://github.com/danielgatis/rembg/releases/download/v0.0.0/u2net_human_seg.onnx"
	modelSHA256 = "01eb6a29a5c4d8edb30b56adad9bb3a2a0535338e480724a213e0acfd2d1c73c"

	// ModelSize is the approximate download size in bytes.
	ModelSize = 176_000_000
)

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
	dest := ModelPath()
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelURL, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
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
