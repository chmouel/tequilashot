package commands

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"

	"github.com/chmouel/tequilashot/internal/external"
	"github.com/chmouel/tequilashot/internal/gui"
	"github.com/chmouel/tequilashot/internal/notify"
)

// nameRetries bounds how often a name taken in the meantime is asked for again.
const nameRetries = 5

// askName asks the user for a file name and hands the choice to commit. A
// name taken between the dialogue and the commit is asked for again, with
// an apology, instead of replacing whatever took it. Declining is not an
// error.
func askName(ctx context.Context, icon string, req external.EntryRequest, commit func(target string, overwrite bool) error) error {
	for range nameRetries {
		res, err := external.Entry(ctx, req)
		if errors.Is(err, external.ErrEntryCancelled) {
			return nil
		}
		if err != nil {
			_ = notify.Send(5000, icon, "The name could not be asked for", err.Error())
			return err
		}

		err = commit(filepath.Join(req.Dir, res.Name), res.Overwrite)
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		req.Name = res.Name
		req.Problem = "Regrettably, that name was taken whilst you were choosing."
	}
	return fmt.Errorf("no free name was settled on in %s", req.Dir)
}

// editInto lets satty edit src and commits the result as target. The editor
// writes to a folder of its own, so only a file it really produced, and a
// valid PNG at that, is ever committed; closing it without saving does
// nothing at all.
func editInto(ctx context.Context, src, target string, overwrite bool) error {
	stage, err := os.MkdirTemp(filepath.Dir(target), ".tequilashot-edit-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()

	out := filepath.Join(stage, "edited.png")
	if err := external.Satty(ctx, src, out, true); err != nil {
		return err
	}
	if !validPNG(out) {
		return nil
	}
	return gui.CommitFile(out, target, overwrite)
}

// validPNG reports whether path holds a non-empty, decodable PNG.
func validPNG(path string) bool {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	cfg, err := png.DecodeConfig(f)
	return err == nil && cfg.Width > 0 && cfg.Height > 0
}

// writeSnapshot keeps data in a private file inside dir, which previews,
// naming and editing share so they all see the one image.
func writeSnapshot(dir string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec
		return "", err
	}
	f, err := os.CreateTemp(dir, ".tequilashot-snapshot-*.png")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}
