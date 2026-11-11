// Package fetch downloads model files over HTTPS and verifies their checksum
// before they are put in place.
package fetch

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
)

// RejectInsecureRedirect keeps a download on HTTPS when a mirror redirects.
func RejectInsecureRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refusing to follow a redirect to %s", req.URL)
	}
	return nil
}

// File describes a download.
type File struct {
	URL    string
	SHA256 string
	// Size is the expected size, used for progress when the server sends none.
	Size int64
	// MaxBytes stops a download that grows beyond it, so a faulty mirror
	// cannot fill the disc.
	MaxBytes int64
}

// Verified fetches f into dest, reporting bytes received and the expected
// total. The file is checked before it replaces any existing one, so an
// interrupted or corrupt download leaves nothing behind.
func Verified(ctx context.Context, client *http.Client, f File, dest string, progress func(done, total int64)) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
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
		total = f.Size
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
			if done > f.MaxBytes {
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
	if hex.EncodeToString(hash.Sum(nil)) != f.SHA256 {
		return fmt.Errorf("the downloaded model is corrupt")
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}
