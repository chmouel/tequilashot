// Package denoise removes background noise from the sound of a recording with
// the DeepFilterNet3 speech model, after toning down the keyboard clicks whose
// times were noted during recording, leaving the picture untouched.
package denoise

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/tequilashot/internal/fetch"
	"github.com/chmouel/tequilashot/internal/keylog"
	"github.com/chmouel/tequilashot/internal/prefs"

	"golang.org/x/sys/unix"
)

// The DeepFilterNet3 network exported to ONNX as a single graph by soniqo,
// from the official v0.5.6 checkpoint.
const (
	modelURL    = "https://huggingface.co/soniqo/DeepFilterNet3-ONNX/resolve/63d8ba442ba900143c468b798e94a04009b2f0c9/deepfilter.onnx"
	modelSHA256 = "e1157049059434ae0d5857e32c812abea227b975e946b2eb64d001abbce156d3"

	// ModelSize is the approximate download size in bytes.
	ModelSize = 8_608_859
)

// ErrNoAudio reports a file without a sound track.
var ErrNoAudio = errors.New("this recording has no sound")

var (
	maxModelBytes  int64 = 20_000_000
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
	if err := runtimeReady(); err != nil {
		return err
	}
	if !ModelInstalled() {
		return fmt.Errorf("the noise-removal model is not installed at %s", ModelPath())
	}
	if _, err := exec.LookPath(prefs.Bin("ffmpeg")); err != nil {
		return fmt.Errorf("FFmpeg not found: %w", err)
	}
	return ctx.Err()
}

// track describes the first sound track of a file.
type track struct {
	// channels is 0 when the file has no sound.
	channels int
	// disposition holds the track's flags in FFmpeg's -disposition form.
	disposition string
	// duration is how long the whole file lasts.
	duration time.Duration
	// delay is how long after the start of the file the track begins.
	delay time.Duration
}

// probe describes the first sound track of file.
func probe(ctx context.Context, file string) (track, error) {
	out, err := exec.CommandContext(ctx, prefs.Bin("ffprobe"), "-v", "error", "-select_streams", "a", //nolint:gosec
		"-show_entries", "stream=index,channels,start_time:stream_disposition:format=duration,start_time", "-of", "json", file).Output()
	if err != nil {
		if ctx.Err() != nil {
			return track{}, ctx.Err()
		}
		return track{}, fmt.Errorf("inspect %s: %w", file, err)
	}
	var p struct {
		Streams []struct {
			Channels    int            `json:"channels"`
			StartTime   string         `json:"start_time"`
			Disposition map[string]int `json:"disposition"`
		} `json:"streams"`
		Format struct {
			Duration  string `json:"duration"`
			StartTime string `json:"start_time"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return track{}, err
	}
	seconds, _ := strconv.ParseFloat(p.Format.Duration, 64)
	t := track{duration: time.Duration(seconds * float64(time.Second))}
	if len(p.Streams) == 0 {
		return t, nil
	}
	t.channels = max(1, p.Streams[0].Channels)
	trackStart, _ := strconv.ParseFloat(p.Streams[0].StartTime, 64)
	fileStart, _ := strconv.ParseFloat(p.Format.StartTime, 64)
	t.delay = max(0, time.Duration((trackStart-fileStart)*float64(time.Second)))
	var flags []string
	for name, on := range p.Streams[0].Disposition {
		if on != 0 {
			flags = append(flags, name)
		}
	}
	slices.Sort(flags)
	t.disposition = cmp.Or(strings.Join(flags, "+"), "0")
	return t, nil
}

// HasAudio reports whether file has a sound track.
func HasAudio(ctx context.Context, file string) (bool, error) {
	t, err := probe(ctx, file)
	return t.channels > 0, err
}

// Export writes a copy of source with its sound cleaned and returns its path.
// An empty dest puts it beside source as "<name>-denoised" with the same
// extension; an existing file is never replaced. The picture and any other
// streams are copied as they are. Key times noted whilst source was recorded
// are used to tone down keyboard clicks.
func Export(ctx context.Context, source, dest string, progress func(float64)) (string, error) {
	if err := Ready(ctx); err != nil {
		return "", err
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
	keys, _ := keylog.Load(source)
	if err := Clean(ctx, source, tmp, keys, progress); err != nil {
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

// Clean writes source to target, replacing it, with its first sound track
// cleaned. The picture, any other sound tracks and other streams are copied
// as they are. keys are key times on source's timeline, which may be empty.
func Clean(ctx context.Context, source, target string, keys []time.Duration, progress func(float64)) error {
	if err := Ready(ctx); err != nil {
		return err
	}
	audio, err := probe(ctx, source)
	if err != nil {
		return err
	}
	if audio.channels == 0 {
		return ErrNoAudio
	}
	channels := audio.channels
	total := audio.duration.Seconds() * sampleRate
	report := func(base, span float64) func(int64) {
		return func(frames int64) {
			if progress != nil && total > 0 {
				progress(min(0.99, base+span*float64(frames)/total))
			}
		}
	}
	c := &cleaner{channels: channels}
	base := 0.0
	if len(keys) > 0 {
		if c.clicks, err = clickPositions(ctx, source, keys, report(0, 0.1)); err != nil {
			return err
		}
		base = 0.1
	}
	run, closeModel, err := loadModel(ModelPath())
	if err != nil {
		return err
	}
	defer closeModel()
	c.run = run

	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	decoder := exec.CommandContext(ctx, prefs.Bin("ffmpeg"), decodeArgs(source, channels)...) //nolint:gosec
	var decodeErr bytes.Buffer
	decoder.Stderr = &decodeErr
	samples, err := decoder.StdoutPipe()
	if err != nil {
		return err
	}
	encoder := exec.CommandContext(ctx, prefs.Bin("ffmpeg"), encodeArgs(source, target, audio)...) //nolint:gosec
	var encodeErr bytes.Buffer
	encoder.Stderr = &encodeErr
	cleaned, err := encoder.StdinPipe()
	if err != nil {
		return err
	}
	if err := encoder.Start(); err != nil {
		return fmt.Errorf("start FFmpeg: %w", err)
	}
	if err := decoder.Start(); err != nil {
		_ = cleaned.Close()
		cancel()
		_ = encoder.Wait()
		return fmt.Errorf("start FFmpeg: %w", err)
	}
	procErr := c.process(samples, cleaned, report(base, 1-base))
	if procErr != nil {
		cancel()
	}
	_ = cleaned.Close()
	decErr := decoder.Wait()
	encErr := encoder.Wait()
	if err := parent.Err(); err != nil {
		return err
	}
	// A failing encoder breaks the pipe; its own message says why.
	if procErr != nil && encErr != nil && encodeErr.Len() > 0 {
		return fmt.Errorf("FFmpeg: %w: %s", encErr, strings.TrimSpace(encodeErr.String()))
	}
	switch {
	case procErr != nil:
		return procErr
	case decErr != nil:
		return fmt.Errorf("FFmpeg: %w: %s", decErr, strings.TrimSpace(decodeErr.String()))
	case encErr != nil:
		return fmt.Errorf("FFmpeg: %w: %s", encErr, strings.TrimSpace(encodeErr.String()))
	}
	return nil
}

// decodeArgs has FFmpeg write the first sound track of source as raw
// interleaved 48 kHz float samples. Raw samples carry no timestamps, so gaps
// within the track are filled to keep the sound in step, and its delay from
// the start of the file is restored when encoding.
func decodeArgs(source string, channels int) []string {
	return []string{
		"-hide_banner", "-v", "error", "-nostdin", "-i", source,
		"-map", "0:a:0", "-ac", strconv.Itoa(channels),
		"-af", "aresample=" + strconv.Itoa(sampleRate) + ":async=1",
		"-f", "f32le", "pipe:1",
	}
}

// encodeArgs has FFmpeg write target from source with the cleaned samples,
// read from its standard input, in place of the first sound track.
func encodeArgs(source, target string, audio track) []string {
	ext := filepath.Ext(target)
	args := []string{
		"-hide_banner", "-v", "error", "-y", "-i", source,
		"-f", "f32le", "-ar", strconv.Itoa(sampleRate), "-ac", strconv.Itoa(audio.channels), "-i", "pipe:0",
		"-map", "0:v?", "-map", "1:a", "-map", "0:a", "-map", "-0:a:0",
		"-c:v", "copy", "-c:a", "copy",
		// Chapters and metadata belong to the copy as much as the picture
		// does, the cleaned track's included.
		"-map_metadata", "0", "-map_chapters", "0", "-map_metadata:s:a:0", "0:s:a:0",
		// Tags follow the metadata, but flags such as default or
		// visual_impaired must be set again on the new track.
		"-disposition:a:0", audio.disposition,
		// Raw samples start at zero, so the track is put back where it
		// began in the source.
		"-filter:a:0", "asetpts=PTS+" + strconv.FormatFloat(audio.delay.Seconds(), 'f', -1, 64) + "/TB",
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
		args = append(args, "-c:a:0", "libopus", "-b:a:0", "128k")
	case ".mp4", ".m4v", ".mov":
		args = append(args, "-c:a:0", "aac", "-b:a:0", "192k", "-movflags", "+faststart")
	default:
		args = append(args, "-c:a:0", "aac", "-b:a:0", "192k")
	}
	return append(args, target)
}

// clickPositions lines keys up with the sound of source and returns them as
// sample positions, or none when they do not line up with it.
func clickPositions(ctx context.Context, source string, keys []time.Duration, progress func(int64)) ([]int64, error) {
	cmd := exec.CommandContext(ctx, prefs.Bin("ffmpeg"), decodeArgs(source, 1)...) //nolint:gosec
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start FFmpeg: %w", err)
	}
	strength, readErr := onsetStrength(pipe, progress)
	if readErr != nil {
		_, _ = io.Copy(io.Discard, pipe)
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("FFmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if readErr != nil {
		return nil, readErr
	}
	lag, ok := align(keys, strength)
	if !ok {
		return nil, nil
	}
	positions := make([]int64, 0, len(keys))
	for _, k := range keys {
		if p := int64(math.Round((k + lag).Seconds() * sampleRate)); p >= 0 {
			positions = append(positions, p)
		}
	}
	return positions, nil
}

// onsetStrength reads mono float samples from r and returns their onset
// strength envelope.
func onsetStrength(r io.Reader, progress func(int64)) ([]float32, error) {
	var o onsets
	br := bufio.NewReaderSize(r, 1<<20)
	block := make([]byte, 4*sampleRate)
	x := make([]float32, sampleRate)
	var read int64
	for {
		n, err := io.ReadFull(br, block)
		n -= n % 4
		for i := range n / 4 {
			x[i] = math.Float32frombits(binary.LittleEndian.Uint32(block[4*i:]))
		}
		o.add(x[:n/4])
		read += int64(n / 4)
		progress(read)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return o.strength, nil
		}
		if err != nil {
			return nil, err
		}
	}
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
