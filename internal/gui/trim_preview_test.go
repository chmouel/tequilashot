package gui

import (
	"slices"
	"testing"
	"time"
)

func TestTrimPreviewSegments(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selection trimSelection
		kind      trimPreviewKind
		want      []trimSegment
	}{
		{"middle cut", trimSelection{4 * time.Second, 7 * time.Second, 12 * time.Second, true}, trimPreviewCut, []trimSegment{{2 * time.Second, 4 * time.Second}, {7 * time.Second, 9 * time.Second}}},
		{"cut beginning", trimSelection{0, 7 * time.Second, 8 * time.Second, true}, trimPreviewCut, []trimSegment{{7 * time.Second, 8 * time.Second}}},
		{"cut end", trimSelection{time.Second, 8 * time.Second, 8 * time.Second, true}, trimPreviewCut, []trimSegment{{0, time.Second}}},
		{"keep start", trimSelection{3 * time.Second, 9 * time.Second, 12 * time.Second, false}, trimPreviewStart, []trimSegment{{3 * time.Second, 5 * time.Second}}},
		{"keep end", trimSelection{3 * time.Second, 9 * time.Second, 12 * time.Second, false}, trimPreviewEnd, []trimSegment{{7 * time.Second, 9 * time.Second}}},
		{"short start", trimSelection{time.Second, 1500 * time.Millisecond, 2 * time.Second, false}, trimPreviewStart, []trimSegment{{time.Second, 1500 * time.Millisecond}}},
		{"short end", trimSelection{time.Second, 1500 * time.Millisecond, 2 * time.Second, false}, trimPreviewEnd, []trimSegment{{time.Second, 1500 * time.Millisecond}}},
		{"invalid", trimSelection{0, time.Second, time.Second, true}, trimPreviewCut, nil},
		{"wrong mode", trimSelection{0, time.Second, 2 * time.Second, false}, trimPreviewCut, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.selection.previewSegments(tc.kind)
			if (err != nil) != (tc.want == nil) || !slices.Equal(got, tc.want) {
				t.Fatalf("preview spans %v, %v; expected %v", got, err, tc.want)
			}
		})
	}
}
