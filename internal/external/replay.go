package external

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chmouel/tequilashot/internal/prefs"
)

// ReplayExportTimeout includes time spent waiting for another replay operation.
const ReplayExportTimeout = 30 * time.Second

// ErrReplayRejected identifies a confirmed recorder rejection rather than a lost acknowledgement.
var ErrReplayRejected = errors.New("replay recorder rejected the request")

// ValidReplaySeconds limits RAM buffering to the supported duration presets.
func ValidReplaySeconds(seconds int) bool {
	return seconds == 30 || seconds == 60 || seconds == 120
}

func replayArgs(p prefs.Prefs, output, directory, socket string, seconds, width, height int, audio AudioSources) ([]string, error) {
	if !ValidReplaySeconds(seconds) {
		return nil, errors.New("replay duration must be 30, 60 or 120 seconds")
	}
	args, err := gsrArgs(p, "", output, directory, width, height, audio, CameraOverlay{})
	if err != nil {
		return nil, err
	}
	extra, err := prefs.RecorderArgs(p.ReplayArgs, true)
	if err != nil {
		return nil, fmt.Errorf("extra replay arguments: %w", err)
	}
	args = append(args, "-r", strconv.Itoa(seconds), "-replay-storage", "ram",
		"-restart-replay-on-save", "no", "-k", "h264", "-bm", "cbr", "-q", strconv.Itoa(p.ReplayBitrate), "-ipc", socket)
	return append(args, extra...), nil
}

// StartReplay leaves readiness checks and process reaping to the caller.
func StartReplay(ctx context.Context, output, directory, socket string, seconds int, audio AudioSources, stderr io.Writer) (*exec.Cmd, error) {
	helpCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p := prefs.Current()
	bin := p.Program("gpu-screen-recorder")
	help, _ := exec.CommandContext(helpCtx, bin, "--help").CombinedOutput() //nolint:gosec
	if !strings.Contains(string(help), "-ipc ") {
		return nil, errors.New("instant replay requires gpu-screen-recorder with -ipc support; please upgrade it")
	}
	w, h := gsrOutputSize(ctx, bin, output)
	args, err := replayArgs(p, output, directory, socket, seconds, w, h, audio)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGINT}
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, stderr)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// ReplayRequest waits for saves to finish before returning the completed file path.
func ReplayRequest(ctx context.Context, socket, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, ReplayExportTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return "", fmt.Errorf("connecting to replay recorder: %w", err)
	}
	defer func() { _ = conn.Close() }()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return "", err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := json.NewEncoder(conn).Encode(struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}{1, name}); err != nil {
		return "", err
	}
	var reply struct {
		ID     int    `json:"id"`
		Result string `json:"result"`
		Data   string `json:"data"`
	}
	if err := json.NewDecoder(conn).Decode(&reply); err != nil {
		return "", fmt.Errorf("waiting for replay recorder: %w", err)
	}
	if reply.ID != 1 {
		return "", errors.New("replay recorder rejected the request identifier")
	}
	if reply.Result != "ok" {
		return "", fmt.Errorf("%w: %s", ErrReplayRejected, reply.Data)
	}
	return reply.Data, nil
}
