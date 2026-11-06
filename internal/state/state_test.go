package state

import (
	"strings"
	"testing"
)

func TestReplayStateAndWaybar(t *testing.T) {
	s := NewState()
	icons := DefaultIcons()
	icons.Replay = "R"
	s.SetIcons(icons)
	s.SetVirtualCamera(true)
	s.SetPaused(true)
	s.SetReplay(true, "DP-1", 120)
	got := s.GetWaybarStatus()
	if got.Class != "replay" || got.Alt != "replay" || got.Text != "R" || !strings.Contains(got.Tooltip, "DP-1") || !strings.Contains(got.Tooltip, "120") {
		t.Fatalf("unexpected replay status: %+v", got)
	}
	if snap := s.GetState(); !snap.Replay || snap.ReplaySeconds != 120 || snap.ReplayOutput != "DP-1" || snap.Recording || snap.Paused {
		t.Fatalf("unexpected state: %+v", snap)
	}
	s.SetCountdown(3)
	if s.GetWaybarStatus().Class != "countdown" {
		t.Fatal("replay hid a capture countdown")
	}
	s.ClearCountdown()
	s.SetReplay(false, "", 0)
	if snap := s.GetState(); snap.Replay || snap.ReplaySeconds != 0 || snap.ReplayOutput != "" {
		t.Fatalf("replay state did not clear: %+v", snap)
	}
	if s.GetWaybarStatus().Class != "live" {
		t.Fatal("stopping replay hid the virtual camera")
	}
}

func TestQuietFinishClearedWhenRecordingEnds(t *testing.T) {
	s := NewState()
	s.SetRecording(true, "x.mp4", 1)
	s.SetQuietFinish(true)
	if !s.QuietFinish() {
		t.Fatal("flag should be set whilst recording")
	}
	s.SetRecording(false, "", 0)
	if s.QuietFinish() {
		t.Fatal("flag should be cleared once recording ends")
	}
}

func TestWaybarShowsVirtualCameraBelowRecording(t *testing.T) {
	s := NewState()
	s.SetVirtualCamera(true)
	if got := s.GetWaybarStatus().Class; got != "live" {
		t.Errorf("class = %q, want live", got)
	}
	s.SetRecording(true, "x.mp4", 1)
	if got := s.GetWaybarStatus().Class; got != "recording" {
		t.Errorf("class = %q, want recording", got)
	}
}
