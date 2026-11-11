package keylog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// Key times are kept beside the state of the program rather than in the
// recording, under the recording's device and inode, so that they follow it
// through renames and moves within its file system. The recording's size and
// modification time are kept as well: a recording that has been changed, or
// a new file reusing the inode, no longer matches.
const (
	magic  = "TQK1"
	maxAge = 90 * 24 * time.Hour
)

// ErrNone reports a recording without key times.
var ErrNone = errors.New("no key times for this recording")

func stateDir() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "tequilashot", "keytimes")
}

type identity struct {
	name  string
	size  int64
	mtime int64
}

func identify(file string) (identity, error) {
	var st unix.Stat_t
	if err := unix.Stat(file, &st); err != nil {
		return identity{}, err
	}
	return identity{
		name:  fmt.Sprintf("%x-%x", st.Dev, st.Ino),
		size:  st.Size,
		mtime: st.Mtim.Nano(),
	}, nil
}

// Save stores times for file. Nothing is stored when times is empty.
func Save(file string, times []time.Duration) error {
	if len(times) == 0 {
		return nil
	}
	id, err := identify(file)
	if err != nil {
		return err
	}
	dir := stateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	prune(dir)
	var b bytes.Buffer
	b.WriteString(magic)
	b.Write(binary.AppendVarint(nil, id.size))
	b.Write(binary.AppendVarint(nil, id.mtime))
	b.Write(binary.AppendUvarint(nil, uint64(len(times))))
	var prev time.Duration
	for _, t := range times {
		d := max(0, (t-prev)/time.Millisecond)
		b.Write(binary.AppendUvarint(nil, uint64(d)))
		prev += d * time.Millisecond
	}
	tmp, err := os.CreateTemp(dir, ".keytimes-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, id.name))
}

// Load returns the key times stored for file, or ErrNone.
func Load(file string) ([]time.Duration, error) {
	id, err := identify(file)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(stateDir(), id.name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNone
	} else if err != nil {
		return nil, err
	}
	r := bytes.NewReader(data)
	head := make([]byte, len(magic))
	if _, err := r.Read(head); err != nil || string(head) != magic {
		return nil, ErrNone
	}
	size, err1 := binary.ReadVarint(r)
	mtime, err2 := binary.ReadVarint(r)
	count, err3 := binary.ReadUvarint(r)
	if err := errors.Join(err1, err2, err3); err != nil || size != id.size || mtime != id.mtime || count > uint64(len(data)) {
		return nil, ErrNone
	}
	times := make([]time.Duration, 0, count)
	var at time.Duration
	for range count {
		d, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, ErrNone
		}
		at += time.Duration(d) * time.Millisecond //nolint:gosec // bounded by the file
		times = append(times, at)
	}
	return times, nil
}

// prune removes key times nobody has written for a long while.
func prune(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
