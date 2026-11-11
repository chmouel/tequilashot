package denoise

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"slices"
)

// The sound is cleaned a segment at a time so that memory stays bounded
// however long the recording. Each segment is processed with some sound
// before it, for the network to settle, and after it, for its lookahead and
// for clicks just past its end; neighbouring segments cross-fade briefly.
const (
	segmentLen = 30 * sampleRate
	warmupLen  = 3 * sampleRate
	tailLen    = repairReach + 100*sampleRate/1000
	xfadeLen   = 10 * sampleRate / 1000
)

// cleaner removes noise from interleaved 48 kHz float samples.
type cleaner struct {
	channels int
	run      model
	// clicks holds the sample positions of the key times, in order.
	clicks []int64
}

// process reads interleaved little-endian float32 samples from r and writes
// them cleaned to w. progress receives the number of frames written.
func (c *cleaner) process(r io.Reader, w io.Writer, progress func(int64)) error {
	br := bufio.NewReaderSize(r, 1<<20)
	bw := bufio.NewWriterSize(w, 1<<20)
	buf := make([][]float32, c.channels)
	var bufStart, s int64
	var prevTail [][]float32
	eof := false
	block := make([]byte, 4*c.channels*4096)
	for {
		for !eof && int64(len(buf[0]))+bufStart < s+segmentLen+tailLen+xfadeLen {
			n, err := io.ReadFull(br, block)
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				eof = true
			} else if err != nil {
				return err
			}
			for off := 0; off+4*c.channels <= n; off += 4 * c.channels {
				for ch := range buf {
					buf[ch] = append(buf[ch], math.Float32frombits(binary.LittleEndian.Uint32(block[off+4*ch:])))
				}
			}
		}
		hi := bufStart + int64(len(buf[0]))
		if s >= hi {
			break
		}
		e := min(s+segmentLen, hi)
		if eof && hi-e < tailLen {
			e = hi
		}
		cleaned, err := c.window(buf, bufStart)
		if err != nil {
			return err
		}
		outEnd := min(hi, e+xfadeLen)
		if e == hi {
			outEnd = hi
		}
		seg := make([][]float32, c.channels)
		for ch := range seg {
			seg[ch] = slices.Clone(cleaned[ch][s-bufStart : outEnd-bufStart])
			if prevTail != nil {
				for i, p := range prevTail[ch] {
					g := (float32(i) + 0.5) / float32(len(prevTail[ch]))
					seg[ch][i] = p*(1-g) + seg[ch][i]*g
				}
			}
		}
		out := make([]byte, 4*c.channels)
		for i := range e - s {
			for ch := range seg {
				binary.LittleEndian.PutUint32(out[4*ch:], math.Float32bits(seg[ch][i]))
			}
			if _, err := bw.Write(out); err != nil {
				return err
			}
		}
		prevTail = nil
		if outEnd > e {
			prevTail = make([][]float32, c.channels)
			for ch := range seg {
				prevTail[ch] = slices.Clone(seg[ch][e-s:])
			}
		}
		if progress != nil {
			progress(e)
		}
		s = e
		if drop := max(0, s-warmupLen) - bufStart; drop > 0 {
			for ch := range buf {
				buf[ch] = slices.Clone(buf[ch][drop:])
			}
			bufStart += drop
		}
		if s == hi && eof {
			break
		}
	}
	return bw.Flush()
}

// window cleans the samples in buf, which start at frame start: first the
// key clicks, then the noise. Channels that are identical are cleaned once.
func (c *cleaner) window(buf [][]float32, start int64) ([][]float32, error) {
	x := make([][]float32, len(buf))
	for ch := range buf {
		x[ch] = slices.Clone(buf[ch])
	}
	c.repair(x, start)
	out := make([][]float32, len(x))
	for ch := range x {
		for prev := range ch {
			if slices.Equal(x[prev], x[ch]) {
				out[ch] = out[prev]
				break
			}
		}
		if out[ch] != nil {
			continue
		}
		y, err := enhance(x[ch], c.run)
		if err != nil {
			return nil, err
		}
		out[ch] = y
	}
	return out, nil
}

// repair tones down the key clicks within x, whose first frame is start.
func (c *cleaner) repair(x [][]float32, start int64) {
	end := start + int64(len(x[0]))
	from, _ := slices.BinarySearch(c.clicks, start)
	if from == len(c.clicks) || c.clicks[from] >= end {
		return
	}
	mix := x[0]
	if len(x) > 1 {
		mix = make([]float32, len(x[0]))
		for _, ch := range x {
			for i, v := range ch {
				mix[i] += v / float32(len(x))
			}
		}
	}
	for _, p := range c.clicks[from:] {
		if p >= end {
			break
		}
		q, ok := findClick(mix, int(p-start))
		if !ok {
			continue
		}
		for _, ch := range x {
			repairClick(ch, q)
		}
		if len(x) > 1 {
			repairClick(mix, q)
		}
	}
}
