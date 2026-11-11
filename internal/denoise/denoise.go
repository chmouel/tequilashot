// Package denoise removes background noise from the sound of a recording with
// FFmpeg's arnndn filter and an RNNoise model, leaving the picture untouched.
package denoise

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/tequilashot/internal/fetch"
	"github.com/chmouel/tequilashot/internal/prefs"

	"golang.org/x/sys/unix"
)

// The "somnolent-hogwash" model from rnnoise-models, trained for speech over
// recording noise.
const (
	modelURL    = "https://raw.githubusercontent.com/GregorR/rnnoise-models/3eee541a283fd3b8f81b85b1748e3b9ccbefa04d/somnolent-hogwash-2018-09-01/sh.rnnn"
	modelSHA256 = "70bb6685eb0c2a1d18e2918dca3fbfbd39317010b1802eb1b6ea73a92f3fdec0"

	// ModelSize is the approximate download size in bytes.
	ModelSize = 300_000
)

// ErrNoAudio reports a file without a sound track.
var ErrNoAudio = errors.New("this recording has no sound")

var (
	maxModelBytes  int64 = 2_000_000
	downloadClient       = &http.Client{CheckRedirect: fetch.RejectInsecureRedirect}
)

// ModelPath returns the model file to use.
func ModelPath() string { return prefs.DenoiseModelPath() }

// ModelInstalled reports whether the model file is in place.
func ModelInstalled() bool {
	_, err := os.Stat(ModelPath())
	return err == nil
}

// DownloadModel fetches the model into ModelPath.
func DownloadModel(ctx context.Context, progress func(done, total int64)) error {
	return DownloadModelTo(ctx, ModelPath(), progress)
}

// DownloadModelTo is DownloadModel with an explicit destination.
func DownloadModelTo(ctx context.Context, dest string, progress func(done, total int64)) error {
	return fetch.Verified(ctx, downloadClient, fetch.File{
		URL: modelURL, SHA256: modelSHA256, Size: ModelSize, MaxBytes: maxModelBytes,
	}, dest, progress)
}

// Ready returns an error explaining why noise removal cannot run.
func Ready(ctx context.Context) error {
	if !ModelInstalled() {
		return fmt.Errorf("the noise-removal model is not installed at %s", ModelPath())
	}
	out, err := exec.CommandContext(ctx, prefs.Bin("ffmpeg"), "-hide_banner", "-filters").Output() //nolint:gosec
	if err != nil {
		return fmt.Errorf("run FFmpeg: %w", err)
	}
	present := make(map[string]bool)
	for line := range strings.Lines(string(out)) {
		if f := strings.Fields(line); len(f) > 1 {
			present[f[1]] = true
		}
	}
	for _, name := range []string{"arnndn", clickFilter} {
		if !present[name] {
			return fmt.Errorf("this FFmpeg build lacks the %s filter", name)
		}
	}
	return nil
}

// escape backslash-escapes the quoting characters and the given specials.
func escape(s, special string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\\' || r == '\'' || strings.ContainsRune(special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// clickFilter removes short impulsive bursts such as keyboard clicks, which
// arnndn leaves in place. Threshold and burst fusion keep their defaults: a
// lower threshold damages speech, and fusing more bursts alters more of it.
const clickFilter = "adeclick"

// clickDucker is the opt-in stage for clicks that overlap speech, which
// neither arnndn nor adeclick can tell from the voice. Above 3.5 kHz it
// lowers loud, short bursts by about 8 dB, and dulls the rest of that band
// by about 2 dB.
const clickDucker = `mcompand=0.005\,0.1 1 -80/-80\,0/0 3500|0.0002\,0.02 1 -80/-80\,-35/-35\,-25/-55\,0/-85 0`

// Filter returns the filter chain for model, with the model path escaped for
// use in a filter graph: once as an option value and once more for the graph
// itself.
//
// arnndn works in 480-sample blocks and fills a short final block from
// uninitialised memory, which often ends the output with NaN samples that
// encoders reject. Padding the last block with silence first avoids that.
func Filter(model string) string {
	chain := "aresample=48000,asetnsamples=n=480:p=1,arnndn=m=" + escape(escape(model, ":"), "[],;") + "," + clickFilter
	if prefs.Current().ReduceClicks {
		chain += "," + clickDucker
	}
	return chain
}

// probe reports whether file has sound and how long it lasts.
func probe(ctx context.Context, file string) (bool, time.Duration, error) {
	out, err := exec.CommandContext(ctx, prefs.Bin("ffprobe"), "-v", "error", "-select_streams", "a", //nolint:gosec
		"-show_entries", "stream=index:format=duration", "-of", "json", file).Output()
	if err != nil {
		if ctx.Err() != nil {
			return false, 0, ctx.Err()
		}
		return false, 0, fmt.Errorf("inspect %s: %w", file, err)
	}
	var p struct {
		Streams []json.RawMessage `json:"streams"`
		Format  struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return false, 0, err
	}
	seconds, _ := strconv.ParseFloat(p.Format.Duration, 64)
	return len(p.Streams) > 0, time.Duration(seconds * float64(time.Second)), nil
}

// HasAudio reports whether file has a sound track.
func HasAudio(ctx context.Context, file string) (bool, error) {
	audio, _, err := probe(ctx, file)
	return audio, err
}

// Export writes a copy of source with its sound cleaned and returns its path.
// An empty dest puts it beside source as "<name>-denoised" with the same
// extension; an existing file is never replaced. The picture and any other
// streams are copied as they are.
func Export(ctx context.Context, source, dest string, progress func(float64)) (string, error) {
	if err := Ready(ctx); err != nil {
		return "", err
	}
	audio, duration, err := probe(ctx, source)
	if err != nil {
		return "", err
	}
	if !audio {
		return "", ErrNoAudio
	}
	target := dest
	if target == "" {
		target = source
	}
	ext := filepath.Ext(target)
	f, err := os.CreateTemp(filepath.Dir(target), ".tequilashot-denoise-*"+ext)
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Close(); err != nil {
		return "", err
	}
	st, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	args := []string{
		"-hide_banner", "-v", "error", "-nostdin", "-y", "-i", source,
		"-map", "0:v?", "-map", "0:a", "-c:v", "copy", "-af", Filter(ModelPath()),
		// Chapters and global metadata belong to the copy as much as the
		// picture does.
		"-map_metadata", "0", "-map_chapters", "0",
	}
	// Other streams (subtitles, attachments, data) are copied when the target
	// can carry them: always for the source's own container, and for
	// Matroska, which accepts anything. Redirection to another container keeps
	// the streams its muxer may reject out of the copy.
	if keepsOtherStreams(source, ext) {
		args = append(args, "-map", "0:s?", "-map", "0:t?", "-map", "0:d?", "-c:s", "copy", "-c:t", "copy", "-c:d", "copy")
	}
	switch strings.ToLower(ext) {
	case ".webm":
		args = append(args, "-c:a", "libopus", "-b:a", "128k")
	case ".mp4", ".m4v", ".mov":
		args = append(args, "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart")
	default:
		args = append(args, "-c:a", "aac", "-b:a", "192k")
	}
	args = append(args, "-progress", "pipe:1", "-nostats", tmp)
	cmd := exec.CommandContext(ctx, prefs.Bin("ffmpeg"), args...) //nolint:gosec
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start FFmpeg: %w", err)
	}
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "out_time_us="); ok && progress != nil && duration > 0 {
			if us, err := strconv.ParseInt(value, 10, 64); err == nil {
				progress(min(0.99, max(0, float64(us)/float64(duration.Microseconds()))))
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("FFmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	// CreateTemp makes the file private and FFmpeg must be able to write it,
	// so the source's permissions are applied only once it is complete.
	if err := os.Chmod(tmp, st.Mode().Perm()); err != nil {
		return "", err
	}
	if dest != "" {
		if err := unix.Renameat2(unix.AT_FDCWD, tmp, unix.AT_FDCWD, dest, unix.RENAME_NOREPLACE); err != nil {
			return "", fmt.Errorf("save %s: %w", dest, err)
		}
		return dest, nil
	}
	return placeBeside(ctx, tmp, source, "-denoised")
}

// keepsOtherStreams reports whether streams beyond picture and sound can be
// copied into a file with extension ext without the muxer refusing them.
func keepsOtherStreams(source, ext string) bool {
	return strings.EqualFold(ext, filepath.Ext(source)) || strings.EqualFold(ext, ".mkv") || strings.EqualFold(ext, ".matroska")
}

// placeBeside renames tmp to source's name plus suffix, adding a number when
// that name is taken, and returns the new path.
func placeBeside(ctx context.Context, tmp, source, suffix string) (string, error) {
	ext := filepath.Ext(source)
	base := strings.TrimSuffix(source, ext) + suffix
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		target := base + ext
		if n > 1 {
			target = fmt.Sprintf("%s-%d%s", base, n, ext)
		}
		if err := unix.Renameat2(unix.AT_FDCWD, tmp, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return "", err
		}
		return target, nil
	}
}
