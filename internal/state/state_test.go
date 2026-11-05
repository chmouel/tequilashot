package state

import "testing"

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
