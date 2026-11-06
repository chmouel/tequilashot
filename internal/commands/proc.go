package commands

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// findRecorderPID returns the most recently started process whose command-line
// arguments contain -o immediately followed by output. procRoot is injectable
// so the scanner can be tested without inspecting the live process table.
func findRecorderPID(ctx context.Context, procRoot, output string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(procRoot) //nolint:gosec
	if err != nil {
		return 0, err
	}

	var newestPID int
	var newestStart uint64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}

		processDir := filepath.Join(procRoot, entry.Name())
		// #nosec G304 -- processDir is constrained to numeric /proc entries.
		cmdline, err := os.ReadFile(filepath.Join(processDir, "cmdline"))
		if err != nil || !hasOutputArgument(cmdline, output) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		// #nosec G304 -- processDir is constrained to numeric /proc entries.
		stat, err := os.ReadFile(filepath.Join(processDir, "stat"))
		if err != nil {
			continue
		}
		start, err := processStartTime(stat)
		if err != nil {
			continue
		}
		if newestPID == 0 || start > newestStart || (start == newestStart && pid > newestPID) {
			newestPID, newestStart = pid, start
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if newestPID == 0 {
		return 0, fmt.Errorf("recorder not found")
	}
	return newestPID, nil
}

func hasOutputArgument(cmdline []byte, output string) bool {
	args := bytes.Split(cmdline, []byte{0})
	for i := 0; i+1 < len(args); i++ {
		if string(args[i]) == "-o" && string(args[i+1]) == output {
			return true
		}
	}
	return false
}

// processStartTime reads field 22 (starttime) from a Linux /proc/<pid>/stat
// record. The process name can itself contain spaces and closing parentheses,
// so parsing starts after its final closing parenthesis.
func processStartTime(stat []byte) (uint64, error) {
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, fmt.Errorf("invalid process stat")
	}
	fields := bytes.Fields(stat[end+1:])
	// fields starts at field 3 (state), so field 22 has index 19.
	if len(fields) <= 19 {
		return 0, fmt.Errorf("invalid process stat")
	}
	return strconv.ParseUint(string(fields[19]), 10, 64)
}
