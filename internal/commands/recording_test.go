package commands

import (
	"context"
	"testing"

	"sway-easyshot/internal/external"
)

func TestMovieGeometryRejectsInvalid(t *testing.T) {
	h := NewRecordingHandler(nil, nil)
	for _, g := range []string{"", "nonsense", "0,0 0x0", "1,2 -3x4"} {
		if err := h.MovieGeometry(context.Background(), 0, g, external.AudioSources{}); err == nil {
			t.Errorf("expected error for %q", g)
		}
	}
}
