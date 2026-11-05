package external

import (
	"bytes"
	"testing"
)

// jpegOf builds a minimal well-formed JPEG whose APP0 segment carries payload.
func jpegOf(payload ...byte) []byte {
	out := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, byte(len(payload) + 2)}
	out = append(out, payload...)
	out = append(out, 0xFF, 0xDA, 0, 4, 0, 0, 0x11, 0xFF, 0x00, 0xFF, 0xD0, 0x22)
	return append(out, 0xFF, 0xD9)
}

func TestJPEGSplitterIgnoresEOIInsideSegments(t *testing.T) {
	a := jpegOf(1, 0xFF, 0xD9, 2, 0xFF, 0xD8, 3)
	b := jpegOf(4)
	stream := append(append([]byte{}, a...), b...)
	for cut := 1; cut < len(stream); cut++ {
		var s JPEGSplitter
		got := s.Feed(stream[:cut])
		got = append(got, s.Feed(stream[cut:])...)
		if len(got) != 2 || !bytes.Equal(got[0], a) || !bytes.Equal(got[1], b) {
			t.Fatalf("cut %d: got %d frames %v", cut, len(got), got)
		}
	}
}

func TestJPEGSplitterMultipleFramesPerRead(t *testing.T) {
	a, b := jpegOf(1, 2, 3), jpegOf(4)
	var s JPEGSplitter
	got := s.Feed(append(append([]byte{}, a...), b...))
	if len(got) != 2 || !bytes.Equal(got[0], a) || !bytes.Equal(got[1], b) {
		t.Fatalf("got %v", got)
	}
}

func TestJPEGSplitterFrameSplitAcrossReads(t *testing.T) {
	a := jpegOf(1, 2, 3, 4, 5)
	for cut := 1; cut < len(a); cut++ {
		var s JPEGSplitter
		got := s.Feed(a[:cut])
		got = append(got, s.Feed(a[cut:])...)
		if len(got) != 1 || !bytes.Equal(got[0], a) {
			t.Fatalf("cut %d: got %v", cut, got)
		}
	}
}

func TestJPEGSplitterByteAtATime(t *testing.T) {
	stream := append(append([]byte{9, 9}, jpegOf(1, 2)...), jpegOf(3)...)
	var s JPEGSplitter
	var got [][]byte
	for _, c := range stream {
		got = append(got, s.Feed([]byte{c})...)
	}
	if len(got) != 2 || !bytes.Equal(got[0], jpegOf(1, 2)) || !bytes.Equal(got[1], jpegOf(3)) {
		t.Fatalf("got %v", got)
	}
}

func TestJPEGSplitterSkipsGarbage(t *testing.T) {
	a := jpegOf(7)
	var s JPEGSplitter
	got := s.Feed(append(append([]byte{1, 2, 0xFF, 3, 0xD9}, a...), 5, 6))
	if len(got) != 1 || !bytes.Equal(got[0], a) {
		t.Fatalf("got %v", got)
	}
	if got := s.Feed([]byte{0, 0}); len(got) != 0 {
		t.Fatalf("garbage produced %v", got)
	}
}

func TestJPEGSplitterCapsOversizeFrame(t *testing.T) {
	var s JPEGSplitter
	chunk := make([]byte, 1<<20)
	copy(chunk, []byte{0xFF, 0xD8, 0xFF, 0xDA, 0, 2})
	for i := 0; i < 6; i++ {
		if got := s.Feed(chunk); len(got) != 0 {
			t.Fatalf("unexpected frame %d", len(got))
		}
		clear(chunk[:6])
	}
	if len(s.buf) > MaxPreviewFrame+len(chunk) {
		t.Fatalf("buffer grew to %d", len(s.buf))
	}
	a := jpegOf(1)
	got := s.Feed(a)
	if len(got) != 1 || !bytes.Equal(got[0], a) {
		t.Fatalf("no recovery after oversize frame: %v", got)
	}
}

func TestLatestFrameDropsStale(t *testing.T) {
	var l LatestFrame
	if !l.Put([]byte{1}) {
		t.Fatal("first Put should request a wake-up")
	}
	if l.Put([]byte{2}) {
		t.Fatal("second Put should not queue another wake-up")
	}
	if got := l.Take(); !bytes.Equal(got, []byte{2}) {
		t.Fatalf("got %v", got)
	}
	if l.Take() != nil || !l.Put([]byte{3}) {
		t.Fatal("Take should re-arm Put")
	}
}
