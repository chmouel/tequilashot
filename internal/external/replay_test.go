package external

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/tequilashot/internal/prefs"
)

func TestReplayArgs(t *testing.T) {
	audio := AudioSources{Desktop: true, Mic: true, MicDevice: "alsa_input.desk"}
	args, err := replayArgs(prefs.Default(), "DP-1", "/clips", "/run/replay.sock", 60, 2560, 1440, audio)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for i := 0; i < len(args); i += 2 {
		got[args[i]] = args[i+1]
	}
	want := map[string]string{
		"-f": "60", "-c": "mp4", "-o": "/clips", "-w": "DP-1", "-s": "1920x1440",
		"-a": "default_output|device:alsa_input.desk", "-r": "60", "-replay-storage": "ram",
		"-restart-replay-on-save": "no", "-k": "h264", "-bm": "cbr", "-q": "20000", "-ipc": "/run/replay.sock",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %v, want %v", got, want)
	}
	for _, seconds := range []int{0, 29, 31, 121} {
		if _, err := replayArgs(prefs.Default(), "DP-1", "/clips", "/run/replay.sock", seconds, 0, 0, AudioSources{}); err == nil {
			t.Errorf("accepted duration %d", seconds)
		}
	}
	for _, seconds := range []int{30, 60, 120} {
		if _, err := replayArgs(prefs.Default(), "DP-1", "/clips", "/run/replay.sock", seconds, 0, 0, AudioSources{}); err != nil {
			t.Errorf("duration %d: %v", seconds, err)
		}
	}
}

func TestReplayRequest(t *testing.T) {
	for _, tc := range []struct {
		name, reply, want, failure string
	}{
		{"saved", `{"id":1,"result":"ok","data":"/clips/replay.mp4"}`, "/clips/replay.mp4", ""},
		{"rejected", `{"id":1,"result":"error","data":"disk full"}`, "", "disk full"},
		{"wrong id", `{"id":2,"result":"ok"}`, "", "rejected"},
		{"invalid", `invalid`, "", "waiting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "gsr.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			requests := make(chan map[string]any, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				var req map[string]any
				_ = json.NewDecoder(conn).Decode(&req)
				requests <- req
				_, _ = conn.Write([]byte(tc.reply + "\n"))
			}()
			file, err := ReplayRequest(context.Background(), socket, "save-replay")
			if file != tc.want || (tc.failure == "" && err != nil) || (tc.failure != "" && (err == nil || !strings.Contains(err.Error(), tc.failure))) {
				t.Fatalf("got %q, %v", file, err)
			}
			req := <-requests
			if req["name"] != "save-replay" || req["id"] != float64(1) {
				t.Fatalf("unexpected IPC request: %v", req)
			}
		})
	}
}

func TestReplayRequestCancellation(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "gsr.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var req any
		_ = json.NewDecoder(conn).Decode(&req)
		cancel()
		var reply any
		_ = json.NewDecoder(conn).Decode(&reply)
	}()
	start := time.Now()
	if _, err := ReplayRequest(ctx, socket, "save-replay"); err == nil {
		t.Fatal("cancelled save reported success")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation did not interrupt the IPC read")
	}
	<-done
}
