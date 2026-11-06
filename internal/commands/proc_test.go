package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeProcProcess(t *testing.T, root string, pid int, args []string, start uint64) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.Join(args, "\x00")+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = "S"
	fields[19] = strconv.FormatUint(start, 10)
	stat := strconv.Itoa(pid) + " (recorder with ) punctuation) " + strings.Join(fields, " ")
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFindRecorderPIDUsesOutputArgumentAndStartTime(t *testing.T) {
	root := t.TempDir()
	output := "/recordings/capture.mp4"
	writeProcProcess(t, root, 901, []string{"recorder", "-o", output}, 50)
	writeProcProcess(t, root, 102, []string{"recorder", "-o", output}, 100)
	writeProcProcess(t, root, 999, []string{"recorder", "-o", output + ".part"}, 200)
	writeProcProcess(t, root, 998, []string{"recorder", "--output", output}, 200)
	writeProcProcess(t, root, 997, []string{"recorder", "prefix-o", output}, 200)

	got, err := findRecorderPID(context.Background(), root, output)
	if err != nil || got != 102 {
		t.Fatalf("findRecorderPID() = %d, %v; want 102, nil", got, err)
	}
}

func TestFindRecorderPIDSkipsRacingProcesses(t *testing.T) {
	root := t.TempDir()
	output := "/recordings/capture.mp4"
	writeProcProcess(t, root, 101, []string{"recorder", "-o", output}, 10)
	if err := os.Mkdir(filepath.Join(root, "202"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "202", "cmdline"), []byte("recorder\x00-o\x00"+output+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeProcProcess(t, root, 303, []string{"recorder", "-o", output}, 30)
	if err := os.Remove(filepath.Join(root, "303", "stat")); err != nil {
		t.Fatal(err)
	}

	got, err := findRecorderPID(context.Background(), root, output)
	if err != nil || got != 101 {
		t.Fatalf("findRecorderPID() = %d, %v; want 101, nil", got, err)
	}
}

func TestFindRecorderPIDHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := findRecorderPID(ctx, t.TempDir(), "/recordings/capture.mp4")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("findRecorderPID() error = %v; want context cancellation", err)
	}
}
