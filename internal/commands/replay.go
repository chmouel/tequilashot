package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/chmouel/tequilashot/internal/external"
)

type replaySession struct {
	cmd           *exec.Cmd
	cancel        context.CancelFunc
	socket        string
	runtime       string
	dir           string
	done          chan struct{}
	cleaned       chan struct{}
	ops           chan struct{}
	stopping      atomic.Bool
	uncertainSave bool
}

type recordingStartKey struct{}

func (h *RecordingHandler) reserveStart(ctx context.Context) (context.Context, func(), error) {
	if ctx.Value(recordingStartKey{}) == h {
		return ctx, func() {}, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.starting || h.replay != nil || h.state.GetState().Recording {
		return ctx, nil, errors.New("a recording or replay session is already active; please stop it first")
	}
	h.starting = true
	return context.WithValue(ctx, recordingStartKey{}, h), func() {
		h.mu.Lock()
		h.starting = false
		h.mu.Unlock()
	}, nil
}

// StartReplay reserves the video slot before asking for a monitor.
func (h *RecordingHandler) StartReplay(ctx context.Context, output string, currentScreen bool, seconds int, audio external.AudioSources) error {
	if !external.ValidReplaySeconds(seconds) {
		return errors.New("replay duration must be 30, 60 or 120 seconds")
	}
	ctx, release, err := h.reserveStart(ctx)
	if err != nil {
		return err
	}
	defer release()
	opts, err := resolveMic(ctx, RecordOptions{Audio: audio})
	if err != nil {
		return err
	}
	if output == "" {
		output, err = selectOutput(ctx, currentScreen)
		if err != nil {
			return fmt.Errorf("could not select a replay monitor: %w", err)
		}
		if output == "" {
			return errors.New("no replay monitor selected")
		}
	}
	runtime, err := os.MkdirTemp(os.Getenv("XDG_RUNTIME_DIR"), "tequilashot-replay-")
	if err != nil {
		return err
	}
	root, err := filepath.Abs(h.cfg.SaveLocation)
	if err != nil {
		_ = os.RemoveAll(runtime)
		return err
	}
	dir, err := os.MkdirTemp(root, ".replay-")
	if err != nil {
		_ = os.RemoveAll(runtime)
		return err
	}
	childCtx, cancel := context.WithCancel(ctx)
	sess := &replaySession{
		cancel: cancel, runtime: runtime, dir: dir, socket: filepath.Join(runtime, "recorder.sock"),
		done: make(chan struct{}), cleaned: make(chan struct{}), ops: make(chan struct{}, 1),
	}
	stderr := &tailBuffer{max: 4096}
	h.mu.Lock()
	h.replay = sess
	h.mu.Unlock()
	cmd, err := external.StartReplay(childCtx, output, dir, sess.socket, seconds, opts.Audio, stderr)
	if err != nil {
		cancel()
		_ = os.RemoveAll(runtime)
		_ = os.RemoveAll(dir)
		h.mu.Lock()
		h.replay = nil
		close(sess.done)
		close(sess.cleaned)
		h.mu.Unlock()
		return fmt.Errorf("could not start replay: %w", err)
	}
	sess.cmd = cmd
	started := time.Now()
	go h.watchReplay(sess, stderr)

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-sess.done:
			return fmt.Errorf("replay recorder stopped during startup: %s", stderr.String())
		case <-ctx.Done():
			sess.stopping.Store(true)
			cancel()
			<-sess.cleaned
			return ctx.Err()
		case <-deadline.C:
			sess.stopping.Store(true)
			cancel()
			<-sess.cleaned
			return fmt.Errorf("replay recorder did not create its IPC socket: %s", stderr.String())
		case <-tick.C:
			if info, err := os.Stat(sess.socket); err == nil && info.Mode()&os.ModeSocket != 0 && time.Since(started) >= earlyExitWindow {
				h.mu.Lock()
				select {
				case <-sess.done:
					h.mu.Unlock()
					return fmt.Errorf("replay recorder stopped during startup: %s", stderr.String())
				default:
				}
				h.state.SetReplay(true, output, seconds)
				h.mu.Unlock()
				_ = sendNotification(2000, h.cfg.RecordingStartIcon, "Replay buffering started", fmt.Sprintf("Keeping the last %d seconds of %s.", seconds, output))
				return nil
			}
		}
	}
}

func (h *RecordingHandler) watchReplay(sess *replaySession, stderr *tailBuffer) {
	err := sess.cmd.Wait()
	h.mu.Lock()
	h.state.SetReplay(false, "", 0)
	close(sess.done)
	h.mu.Unlock()
	sess.ops <- struct{}{}
	// A timed-out IPC save may still have completed before the recorder exited
	entries, _ := os.ReadDir(sess.dir)
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".mp4" {
			continue
		}
		file := filepath.Join(sess.dir, entry.Name())
		if _, saveErr := h.keepReplay(file, sess.dir); saveErr != nil {
			_ = sendNotification(5000, h.cfg.RecordingStopIcon, "Replay file retained", file)
		}
	}
	_ = os.Remove(sess.dir)
	_ = os.RemoveAll(sess.runtime)
	sess.cancel()
	h.mu.Lock()
	if h.replay == sess {
		h.replay = nil
	}
	h.mu.Unlock()
	if !sess.stopping.Load() {
		_ = sendNotification(5000, h.cfg.RecordingStopIcon, "Replay buffering stopped", fmt.Sprintf("%v %s", err, stderr.String()))
	}
	close(sess.cleaned)
}

func (h *RecordingHandler) replayOperation(ctx context.Context) (*replaySession, func(), error) {
	h.mu.Lock()
	sess := h.replay
	starting := h.starting
	h.mu.Unlock()
	if sess == nil || starting || sess.stopping.Load() {
		return nil, nil, errors.New("no replay is ready")
	}
	select {
	case sess.ops <- struct{}{}:
		select {
		case <-sess.done:
			<-sess.ops
			return nil, nil, errors.New("replay buffering has stopped")
		default:
		}
		return sess, func() { <-sess.ops }, nil
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-sess.done:
		return nil, nil, errors.New("replay buffering has stopped")
	}
}

// SaveReplay returns confirmed, uniquely named clips and leaves buffering active.
func (h *RecordingHandler) SaveReplay(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, external.ReplayExportTimeout)
	defer cancel()
	sess, release, err := h.replayOperation(ctx)
	if err != nil {
		return "", err
	}
	file, err := h.saveReplayClip(ctx, sess)
	// Notify only after releasing the operation slot so a slow notifier cannot delay other operations
	release()
	if err != nil {
		return "", err
	}
	_ = sendNotification(2000, h.cfg.RecordingStopIcon, "Replay saved", filepath.Base(file))
	return file, nil
}

// saveReplayClip must be called whilst holding the session operation slot.
func (h *RecordingHandler) saveReplayClip(ctx context.Context, sess *replaySession) (string, error) {
	if sess.uncertainSave {
		return "", errors.New("the previous replay save could not be confirmed; please stop and restart buffering before saving again")
	}
	file, err := external.ReplayRequest(ctx, sess.socket, "save-replay")
	if err != nil {
		// Do not overwrite a late export after losing its acknowledgement
		sess.uncertainSave = !errors.Is(err, external.ErrReplayRejected)
		return "", err
	}
	file, err = h.keepReplay(file, sess.dir)
	if err != nil {
		sess.uncertainSave = true
		return "", err
	}
	return file, nil
}

func (h *RecordingHandler) keepReplay(file, directory string) (string, error) {
	if filepath.Dir(filepath.Clean(file)) != filepath.Clean(directory) || filepath.Ext(file) != ".mp4" {
		return "", errors.New("recorder returned an unexpected replay path")
	}
	info, err := os.Lstat(file)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", errors.New("the replay file is empty or is not a regular file")
	}
	target, err := os.CreateTemp(h.cfg.SaveLocation, "replay-"+time.Now().Format("20060102-150405")+"-*.mp4")
	if err != nil {
		return "", fmt.Errorf("replay retained at %s: %w", file, err)
	}
	name := target.Name()
	if err := target.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	if err := os.Rename(file, name); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("replay retained at %s: %w", file, err)
	}
	return name, nil
}

// StopReplay discards unsaved footage and waits for session resources to be released.
func (h *RecordingHandler) StopReplay(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, external.ReplayExportTimeout)
	defer cancel()
	sess, release, err := h.replayOperation(ctx)
	if err != nil {
		return err
	}
	sess.stopping.Store(true)
	_, err = external.ReplayRequest(ctx, sess.socket, "stop")
	if err != nil {
		_ = sess.cmd.Process.Signal(syscall.SIGINT)
	}
	select {
	case <-sess.done:
	case <-ctx.Done():
		sess.cancel()
	}
	release()
	<-sess.cleaned
	_ = sendNotification(2000, h.cfg.RecordingStopIcon, "Replay buffering stopped", "")
	return nil
}

// ShutdownReplay cancels and reaps only this handler's replay process.
func (h *RecordingHandler) ShutdownReplay() {
	h.mu.Lock()
	sess := h.replay
	if sess != nil {
		sess.stopping.Store(true)
		sess.cancel()
	}
	h.mu.Unlock()
	if sess != nil {
		<-sess.cleaned
	}
}
