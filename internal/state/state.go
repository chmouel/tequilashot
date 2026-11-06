package state

import (
	"fmt"
	"sync"
	"time"

	"github.com/chmouel/tequilashot/pkg/protocol"
)

// State tracks the current state of recordings.
type State struct {
	mu                 sync.RWMutex
	replay             bool
	replayOutput       string
	replaySeconds      int
	recording          bool
	paused             bool
	recordingFile      string
	recordingPID       int
	quietFinish        bool
	recordingStartTime time.Time
	countdownRemaining int
	virtualCamera      bool
	icons              Icons
}

// Icons holds custom icons for different states.
type Icons struct {
	Idle      string
	Recording string
	Paused    string
	Countdown string
	Live      string
	Replay    string
}

// DefaultIcons returns the default icon set.
func DefaultIcons() Icons {
	return Icons{
		Idle:      "",
		Recording: "󰑊",
		Paused:    "󰏤",
		Countdown: "⏱",
		Live:      "󰄀",
		Replay:    "↶",
	}
}

// NewState creates a new state instance with default icons.
func NewState() *State {
	return &State{
		icons: DefaultIcons(),
	}
}

// NewStateWithIcons creates a new State with custom icons.
func NewStateWithIcons(icons Icons) *State {
	return &State{
		icons: icons,
	}
}

// GetState returns the current state snapshot.
func (s *State) GetState() *protocol.State {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return &protocol.State{
		Replay:        s.replay,
		ReplayOutput:  s.replayOutput,
		ReplaySeconds: s.replaySeconds,
		Recording:     s.recording,
		Paused:        s.paused,
		RecordingFile: s.recordingFile,
		VirtualCamera: s.virtualCamera,
	}
}

// SetReplay publishes replay independently of recording and virtual camera state.
func (s *State) SetReplay(active bool, output string, seconds int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replay, s.replayOutput, s.replaySeconds = active, output, seconds
	if active {
		s.paused = false
	}
}

// SetRecording sets the recording state and file information.
func (s *State) SetRecording(recording bool, file string, pid int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recording = recording
	s.recordingFile = file
	s.recordingPID = pid
	if recording {
		s.recordingStartTime = time.Now()
	} else {
		s.recordingStartTime = time.Time{}
		s.quietFinish = false
	}
}

// SetQuietFinish marks the current recording as one whose result is shown by the client.
func (s *State) SetQuietFinish(quiet bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.quietFinish = quiet
}

// QuietFinish reports whether the current recording is flagged quiet.
func (s *State) QuietFinish() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.quietFinish
}

// GetRecordingPID returns the process ID of the current recording.
func (s *State) GetRecordingPID() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recordingPID
}

// SetVirtualCamera records whether the virtual camera is running.
func (s *State) SetVirtualCamera(running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.virtualCamera = running
}

// VirtualCamera reports whether the virtual camera is running.
func (s *State) VirtualCamera() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.virtualCamera
}

// SetPaused sets the pause state of the current recording.
func (s *State) SetPaused(paused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = paused
}

// SetCountdown sets the countdown remaining seconds.
func (s *State) SetCountdown(seconds int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.countdownRemaining = seconds
}

// ClearCountdown clears the countdown state.
func (s *State) ClearCountdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.countdownRemaining = 0
}

// GetWaybarStatus returns the current waybar status representation.
func (s *State) GetWaybarStatus() *protocol.WaybarStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Priority: countdown > screen recorder/replay > virtual camera
	if s.countdownRemaining > 0 {
		return &protocol.WaybarStatus{
			Text:    fmt.Sprintf("%s %d", s.icons.Countdown, s.countdownRemaining),
			Tooltip: fmt.Sprintf("Starting in %d seconds", s.countdownRemaining),
			Class:   "countdown",
			Alt:     "countdown",
		}
	}

	if s.recording {
		if s.paused {
			return &protocol.WaybarStatus{
				Text:    s.icons.Paused,
				Tooltip: "Recording paused",
				Class:   "paused",
				Alt:     "paused",
			}
		}
		elapsed := time.Since(s.recordingStartTime)
		minutes := int(elapsed.Minutes())
		seconds := int(elapsed.Seconds()) % 60
		return &protocol.WaybarStatus{
			Text:    fmt.Sprintf("%s %02d:%02d", s.icons.Recording, minutes, seconds),
			Tooltip: fmt.Sprintf("Recording: %s (%02d:%02d)", s.recordingFile, minutes, seconds),
			Class:   "recording",
			Alt:     "recording",
		}
	}

	if s.replay {
		return &protocol.WaybarStatus{Text: s.icons.Replay, Tooltip: fmt.Sprintf("Replay buffering: %s (last %d seconds)", s.replayOutput, s.replaySeconds), Class: "replay", Alt: "replay"}
	}

	if s.virtualCamera {
		return &protocol.WaybarStatus{
			Text:    s.icons.Live,
			Tooltip: "Virtual camera live",
			Class:   "live",
			Alt:     "live",
		}
	}

	return &protocol.WaybarStatus{
		Text:    s.icons.Idle,
		Tooltip: "Ready for screenshot/recording",
		Class:   "idle",
		Alt:     "idle",
	}
}

// SetIcons updates the icons used for waybar status.
func (s *State) SetIcons(icons Icons) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.icons = icons
}
