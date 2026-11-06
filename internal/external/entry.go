package external

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Exit statuses of the entry command.
const (
	entryExitCancel    = 1
	entryExitOverwrite = 3
)

// ErrEntryCancelled reports that the user declined to name the file.
var ErrEntryCancelled = errors.New("naming cancelled")

// EntryRequest describes a file name to ask the user for.
type EntryRequest struct {
	// Action is "rename", "save" or "edit".
	Action string
	// Dir and Ext locate the file; Ext is fixed.
	Dir, Ext string
	// Name is the stem offered to start with.
	Name string
	// File is previewed and, for a rename, is the file being renamed.
	File string
	// Problem explains why the name is being asked for again.
	Problem string
	// AIModel, when set, has the window suggest a name.
	AIModel string
}

// EntryResult is the name the user accepted.
type EntryResult struct {
	// Name is the complete file name, extension included.
	Name string
	// Overwrite reports that replacing an existing file was agreed to.
	Overwrite bool
}

func entryCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, exe, append([]string{"entry"}, args...)...), nil //nolint:gosec
}

// Entry asks the user for a file name in a window of its own.
func Entry(ctx context.Context, r EntryRequest) (EntryResult, error) {
	args := []string{"--action", r.Action, "--dir", r.Dir, "--ext", r.Ext}
	for flag, value := range map[string]string{
		"--name": r.Name, "--file": r.File, "--problem": r.Problem, "--ai-model": r.AIModel,
	} {
		if value != "" {
			args = append(args, flag, value)
		}
	}
	cmd, err := entryCommand(ctx, args...)
	if err != nil {
		return EntryResult{}, err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()

	res := EntryResult{Name: strings.TrimSuffix(stdout.String(), "\n")}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit) && exit.ExitCode() == entryExitOverwrite:
		res.Overwrite = true
	case errors.As(err, &exit) && exit.ExitCode() == entryExitCancel:
		return EntryResult{}, ErrEntryCancelled
	default:
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return EntryResult{}, fmt.Errorf("naming window: %s", msg)
		}
		return EntryResult{}, fmt.Errorf("naming window: %w", err)
	}
	if res.Name == "" {
		return EntryResult{}, ErrEntryCancelled
	}
	return res, nil
}

var (
	entryProbe    sync.Once
	entrySupports bool
)

// EntrySupported reports whether the naming window can be shown. The check
// runs once; later calls reuse its answer.
func EntrySupported() bool {
	entryProbe.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd, err := entryCommand(ctx, "--probe")
		entrySupports = err == nil && cmd.Run() == nil
	})
	return entrySupports
}
