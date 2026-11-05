package state

import (
	"fmt"
	"sync"
	"time"

	"sway-easyshot/pkg/protocol"
)

// State tracks the current state of recordings and OBS.
type State struct {
	mu                 sync.RWMutex
	recording          bool
	paused             bool
	recordingFile      string
	recordingPID       int
	recordingStartTime time.Time
	obsRecording       bool
	obsPaused          bool
	countdownRemaining int
	icons              Icons
}

// Icons holds custom icons for different states.
type Icons struct {
	Idle         string
	Recording    string
	Paused       string
	ObsRecording string
	ObsPaused    string
	Countdown    string
}

// DefaultIcons returns the default icon set.
func DefaultIcons() Icons {
	return Icons{
		Idle:         "",
		Recording:    "󰑊",
		Paused:       "󰏤",
		ObsRecording: "󰑊",
		ObsPaused:    "󰏤",
		Countdown:    "⏱",
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
		Recording:     s.recording,
		Paused:        s.paused,
		RecordingFile: s.recordingFile,
		OBSRecording:  s.obsRecording,
		OBSPaused:     s.obsPaused,
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
	}
}

// SetOBSState sets the OBS recording and pause state.
func (s *State) SetOBSState(recording, paused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.obsRecording = recording
	s.obsPaused = paused
}

// GetRecordingPID returns the process ID of the current recording.
func (s *State) GetRecordingPID() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recordingPID
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

	// Priority: countdown > screen recorder > OBS
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

	if s.obsRecording {
		if s.obsPaused {
			return &protocol.WaybarStatus{
				Text:    s.icons.ObsPaused,
				Tooltip: "OBS recording paused",
				Class:   "paused",
				Alt:     "paused",
			}
		}
		return &protocol.WaybarStatus{
			Text:    s.icons.ObsRecording,
			Tooltip: "OBS recording in progress",
			Class:   "recording",
			Alt:     "recording",
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
