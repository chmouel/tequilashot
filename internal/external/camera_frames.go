package external

import (
	"bytes"
	"sync"
)

// MaxPreviewFrame bounds the bytes buffered for a single preview frame.
const MaxPreviewFrame = 4 << 20

var jpegSOI = []byte{0xFF, 0xD8}

// JPEGSplitter cuts a concatenated MJPEG stream into single JPEG frames. It
// follows the marker structure, so an FF D9 inside a length-delimited segment
// or entropy-coded data does not end a frame.
type JPEGSplitter struct {
	buf    []byte
	pos    int  // parse position within buf, after the SOI
	inScan bool // pos lies within entropy-coded data
}

// Feed adds data and returns the frames completed by it. Bytes outside a
// frame are discarded, as is a malformed frame or one growing past
// MaxPreviewFrame.
func (s *JPEGSplitter) Feed(data []byte) [][]byte {
	s.buf = append(s.buf, data...)
	var frames [][]byte
	for {
		if len(s.buf) == 0 {
			return frames
		}
		start := bytes.Index(s.buf, jpegSOI)
		if start < 0 {
			// Keep a trailing 0xFF, which may begin a marker split across reads.
			if s.buf[len(s.buf)-1] == 0xFF {
				s.buf = append(s.buf[:0], 0xFF)
			} else {
				s.buf = s.buf[:0]
			}
			s.reset()
			return frames
		}
		if start > 0 {
			s.buf = append(s.buf[:0], s.buf[start:]...)
			s.reset()
		}

		end, status := s.parse()
		switch status {
		case jpegNeedMore:
			if len(s.buf) > MaxPreviewFrame {
				s.buf = s.buf[:0]
				s.reset()
			}
			return frames
		case jpegInvalid:
			s.buf = s.buf[2:]
			s.reset()
		default:
			frames = append(frames, append([]byte(nil), s.buf[:end]...))
			s.buf = append(s.buf[:0], s.buf[end:]...)
			s.reset()
		}
	}
}

func (s *JPEGSplitter) reset() { s.pos, s.inScan = 0, false }

type jpegStatus int

const (
	jpegNeedMore jpegStatus = iota
	jpegInvalid
	jpegDone
)

// parse advances over the frame at the start of buf, resuming where it last
// stopped, and returns the frame length once the EOI marker is reached.
func (s *JPEGSplitter) parse() (int, jpegStatus) {
	if s.pos < 2 {
		s.pos = 2
	}
	b := s.buf
	for {
		if s.inScan {
			i := bytes.IndexByte(b[s.pos:], 0xFF)
			if i < 0 {
				s.pos = len(b)
				return 0, jpegNeedMore
			}
			s.pos += i
			if s.pos+1 >= len(b) {
				return 0, jpegNeedMore
			}
			switch m := b[s.pos+1]; {
			case m == 0x00 || (m >= 0xD0 && m <= 0xD7):
				s.pos += 2
			case m == 0xFF:
				s.pos++
			case m == 0xD9:
				return s.pos + 2, jpegDone
			default:
				s.inScan = false
			}
			continue
		}

		if s.pos+1 >= len(b) {
			return 0, jpegNeedMore
		}
		if b[s.pos] != 0xFF {
			return 0, jpegInvalid
		}
		m := b[s.pos+1]
		switch {
		case m == 0xFF:
			s.pos++
		case m == 0xD9:
			return s.pos + 2, jpegDone
		case m == 0x00:
			return 0, jpegInvalid
		case m == 0x01 || (m >= 0xD0 && m <= 0xD8):
			s.pos += 2
		default:
			if s.pos+3 >= len(b) {
				return 0, jpegNeedMore
			}
			l := int(b[s.pos+2])<<8 | int(b[s.pos+3])
			if l < 2 {
				return 0, jpegInvalid
			}
			if s.pos+2+l > len(b) {
				return 0, jpegNeedMore
			}
			s.pos += 2 + l
			s.inScan = m == 0xDA
		}
	}
}

// LatestFrame hands the newest frame to a consumer, dropping any it has not
// yet collected so that nothing queues up.
type LatestFrame struct {
	mu     sync.Mutex
	data   []byte
	queued bool
}

// Put stores frame and reports whether the consumer needs to be woken; it
// does not when an earlier wake-up is still outstanding.
func (l *LatestFrame) Put(frame []byte) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.data = frame
	if l.queued {
		return false
	}
	l.queued = true
	return true
}

// Take returns the newest frame, or nil, and re-arms Put.
func (l *LatestFrame) Take() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.data
	l.data, l.queued = nil, false
	return d
}
