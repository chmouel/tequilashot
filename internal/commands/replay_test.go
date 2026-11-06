package commands

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/tequilashot/internal/external"
	"github.com/chmouel/tequilashot/internal/state"
)

func TestReplayRecorderProcess(t *testing.T) {
	if os.Getenv("TEQUILASHOT_REPLAY_TEST_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	options := map[string]string{}
	for i := 1; i+1 < len(args); i += 2 {
		options[args[i]] = args[i+1]
	}
	if os.Getenv("TEQUILASHOT_REPLAY_TEST_EXIT") == "1" {
		os.Exit(1)
	}
	listener, err := net.Listen("unix", options["-ipc"])
	if err != nil {
		os.Exit(2)
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			os.Exit(3)
		}
		var req struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			_ = conn.Close()
			continue
		}
		reply := map[string]any{"id": req.ID, "result": "ok"}
		if req.Name == "save-replay" {
			gate := os.Getenv("TEQUILASHOT_REPLAY_TEST_GATE")
			for gate != "" {
				if _, err := os.Stat(gate); err != nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if marker := os.Getenv("TEQUILASHOT_REPLAY_TEST_REJECT"); marker != "" && os.Remove(marker) == nil {
				reply["result"], reply["data"] = "error", "disk full"
			} else {
				file := filepath.Join(options["-o"], "Replay_fixed.mp4")
				if err := os.WriteFile(file, []byte("video"), 0o600); err != nil {
					os.Exit(4)
				}
				reply["data"] = file
			}
		}
		_ = json.NewEncoder(conn).Encode(reply)
		_ = conn.Close()
		if req.Name == "stop" {
			os.Exit(0)
		}
	}
}

func newReplayTestHandler(t *testing.T) (*RecordingHandler, *state.State) {
	t.Helper()
	dir, err := os.MkdirTemp("", "tqr-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("TEQUILASHOT_REPLAY_TEST_HELPER", "1")
	t.Setenv("TEQUILASHOT_REPLAY_TEST_BINARY", os.Args[0])
	script := "#!/bin/sh\ncase \"$1\" in\n--help) echo '-ipc socket'; exit 0;;\n--list-capture-options) exit 0;;\nesac\nexec \"$TEQUILASHOT_REPLAY_TEST_BINARY\" -test.run=^TestReplayRecorderProcess$ -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gpu-screen-recorder"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	originalNotify := sendNotification
	sendNotification = func(int, string, string, string) error { return nil }
	t.Cleanup(func() { sendNotification = originalNotify })
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	h, st := newTestHandler(t)
	t.Cleanup(h.ShutdownReplay)
	return h, st
}

func TestReplayLifecycle(t *testing.T) {
	h, st := newReplayTestHandler(t)
	ctx := context.Background()
	if err := h.StartReplay(ctx, "DP-1", false, 60, external.AudioSources{}); err != nil {
		t.Fatal(err)
	}
	sess := h.replay
	if got := st.GetState(); !got.Replay || got.ReplayOutput != "DP-1" || got.ReplaySeconds != 60 || got.Recording || got.RecordingFile != "" {
		t.Fatalf("unexpected state: %+v", got)
	}
	if err := h.StartReplay(ctx, "DP-1", false, 60, external.AudioSources{}); err == nil {
		t.Fatal("accepted a second replay")
	}
	if err := h.MovieOutput(ctx, 0, "DP-1", 0, RecordOptions{}); err == nil {
		t.Fatal("accepted a recording during replay")
	}
	if err := h.StopRecording(ctx); err == nil || !strings.Contains(err.Error(), "replay-stop") {
		t.Fatalf("ordinary stop did not guard replay: %v", err)
	}
	if err := h.PauseRecording(ctx); err == nil {
		t.Fatal("accepted pause during replay")
	}
	var wg sync.WaitGroup
	files := make(chan string, 2)
	for range 2 {
		wg.Go(func() {
			file, err := h.SaveReplay(ctx)
			if err != nil {
				t.Errorf("save: %v", err)
			}
			files <- file
		})
	}
	wg.Wait()
	first, second := <-files, <-files
	if first == "" || first == second || !st.GetState().Replay {
		t.Fatalf("saves were not distinct or interrupted buffering: %q %q", first, second)
	}
	if err := h.StopReplay(ctx); err != nil {
		t.Fatal(err)
	}
	if st.GetState().Replay {
		t.Fatal("replay state still active after stopping")
	}
	for _, path := range []string{sess.runtime, sess.dir} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("session directory remains: %s: %v", path, err)
		}
	}
	for _, file := range []string{first, second} {
		if data, err := os.ReadFile(file); err != nil || string(data) != "video" { //nolint:gosec
			t.Errorf("saved clip lost after stopping: %q %v", data, err)
		}
	}
	if _, err := h.SaveReplay(ctx); err == nil {
		t.Fatal("accepted a save after stopping")
	}
	if err := h.StartReplay(ctx, "DP-2", false, 30, external.AudioSources{}); err != nil {
		t.Fatalf("could not restart: %v", err)
	}
}

func TestReplayFailedSaveKeepsBuffer(t *testing.T) {
	h, st := newReplayTestHandler(t)
	marker := filepath.Join(t.TempDir(), "reject")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEQUILASHOT_REPLAY_TEST_REJECT", marker)
	if err := h.StartReplay(context.Background(), "DP-1", false, 60, external.AudioSources{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SaveReplay(context.Background()); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected export error: %v", err)
	}
	if !st.GetState().Replay {
		t.Fatal("failed export stopped buffering")
	}
	if _, err := h.SaveReplay(context.Background()); err != nil {
		t.Fatalf("could not retry export: %v", err)
	}
}

func TestReplayCancelledSaveAndShutdown(t *testing.T) {
	h, st := newReplayTestHandler(t)
	gate := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEQUILASHOT_REPLAY_TEST_GATE", gate)
	if err := h.StartReplay(context.Background(), "DP-1", false, 60, external.AudioSources{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := h.SaveReplay(ctx); err == nil {
		t.Fatal("cancelled save reported success")
	}
	if !st.GetState().Replay {
		t.Fatal("cancelled save stopped buffering")
	}
	sess := h.replay
	h.ShutdownReplay()
	if st.GetState().Replay {
		t.Fatal("shutdown left replay state active")
	}
	if _, err := os.Stat(sess.runtime); !os.IsNotExist(err) {
		t.Fatalf("shutdown left runtime files: %v", err)
	}
}

func TestReplayStartupFailureAndCrash(t *testing.T) {
	h, st := newReplayTestHandler(t)
	t.Setenv("TEQUILASHOT_REPLAY_TEST_EXIT", "1")
	if err := h.StartReplay(context.Background(), "DP-1", false, 60, external.AudioSources{}); err == nil {
		t.Fatal("early exit reported success")
	}
	h.ShutdownReplay()
	t.Setenv("TEQUILASHOT_REPLAY_TEST_EXIT", "")
	if err := h.StartReplay(context.Background(), "DP-1", false, 60, external.AudioSources{}); err != nil {
		t.Fatalf("failed startup leaked recorder slot: %v", err)
	}
	sess := h.replay
	if err := sess.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sess.cleaned:
	case <-time.After(5 * time.Second):
		t.Fatal("crashed recorder was not cleaned up")
	}
	if st.GetState().Replay {
		t.Fatal("crashed recorder still shown as buffering")
	}
	st.SetRecording(true, "clip.mp4", 1)
	if err := h.StartReplay(context.Background(), "DP-1", false, 60, external.AudioSources{}); err == nil {
		t.Fatal("accepted replay during a recording")
	}
}

func TestReplayLateSaveRecoveredOnStop(t *testing.T) {
	h, _ := newReplayTestHandler(t)
	// Glob metacharacters in the save location must not hide late exports
	h.cfg.SaveLocation = filepath.Join(h.cfg.SaveLocation, "clips[")
	if err := os.Mkdir(h.cfg.SaveLocation, 0o700); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEQUILASHOT_REPLAY_TEST_GATE", gate)
	if err := h.StartReplay(context.Background(), "DP-1", false, 60, external.AudioSources{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := h.SaveReplay(ctx); err == nil {
		t.Fatal("unconfirmed export reported success")
	}
	if _, err := h.SaveReplay(context.Background()); err == nil || !strings.Contains(err.Error(), "previous replay save") {
		t.Fatalf("allowed another export to overwrite the unconfirmed clip: %v", err)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	if err := h.StopReplay(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(h.cfg.SaveLocation)
	var files []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "replay-") && filepath.Ext(entry.Name()) == ".mp4" {
			files = append(files, entry.Name())
		}
	}
	if err != nil || len(files) != 1 {
		t.Fatalf("late export not recovered: %v %v", files, err)
	}
}

func TestReplaySavedNotificationDoesNotBlockShutdown(t *testing.T) {
	h, _ := newReplayTestHandler(t)
	started, unblock := make(chan struct{}), make(chan struct{})
	sendNotification = func(_ int, _, title, _ string) error {
		if title == "Replay saved" {
			close(started)
			<-unblock
		}
		return nil
	}
	if err := h.StartReplay(context.Background(), "DP-1", false, 60, external.AudioSources{}); err != nil {
		t.Fatal(err)
	}
	saved := make(chan error, 1)
	go func() {
		_, err := h.SaveReplay(context.Background())
		saved <- err
	}()
	defer func() {
		close(unblock)
		if err := <-saved; err != nil {
			t.Errorf("save: %v", err)
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("saved notification was never sent")
	}
	shutdown := make(chan struct{})
	go func() {
		h.ShutdownReplay()
		close(shutdown)
	}()
	select {
	case <-shutdown:
	case <-time.After(2 * time.Second):
		t.Fatal("a pending saved notification blocked replay shutdown")
	}
}

func TestReplayReservesBeforeSelectingMonitor(t *testing.T) {
	h, _ := newReplayTestHandler(t)
	original := selectOutput
	selected := make(chan struct{})
	finish := make(chan struct{})
	selectOutput = func(context.Context, bool) (string, error) {
		close(selected)
		<-finish
		return "", context.Canceled
	}
	defer func() { selectOutput = original }()
	done := make(chan error, 1)
	go func() { done <- h.StartReplay(context.Background(), "", false, 60, external.AudioSources{}) }()
	<-selected
	if err := h.MovieOutput(context.Background(), 0, "DP-1", 0, RecordOptions{}); err == nil {
		t.Error("recording was allowed whilst replay was selecting a monitor")
	}
	close(finish)
	if err := <-done; err == nil {
		t.Fatal("cancelled selection reported success")
	}
	if err := h.StartReplay(context.Background(), "DP-1", false, 60, external.AudioSources{}); err != nil {
		t.Fatalf("cancelled selection leaked the session slot: %v", err)
	}
}

func TestReplayShutdownDuringStartup(t *testing.T) {
	h, st := newReplayTestHandler(t)
	done := make(chan error, 1)
	go func() { done <- h.StartReplay(context.Background(), "DP-1", false, 60, external.AudioSources{}) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		sess := h.replay
		h.mu.Unlock()
		if sess != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("replay startup never reserved its process")
		}
		time.Sleep(time.Millisecond)
	}
	h.ShutdownReplay()
	if err := <-done; err == nil {
		t.Fatal("shutdown during startup reported success")
	}
	if st.GetState().Replay {
		t.Fatal("startup republished replay after shutdown")
	}
}
