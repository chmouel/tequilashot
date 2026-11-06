package prefs

import (
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"", nil, false},
		{"  -ac  opus ", []string{"-ac", "opus"}, false},
		{`-df "a b" 'c d' e\ f`, []string{"-df", "a b", "c d", "e f"}, false},
		{`-v "" `, []string{"-v", ""}, false},
		{`'$(rm -rf ~)'`, []string{"$(rm -rf ~)"}, false},
		{`"open`, nil, true},
		{`trail\`, nil, true},
	}
	for _, tt := range tests {
		got, err := SplitArgs(tt.in)
		if (err != nil) != tt.wantErr || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitArgs(%q) = %q, %v", tt.in, got, err)
		}
	}
}

func TestRecorderArgs(t *testing.T) {
	tests := []struct {
		in     string
		replay bool
		ok     bool
	}{
		{"", false, true},
		{"-ac opus -cr full", false, true},
		{"-bm vbr", false, true},
		{"-bm vbr", true, false},
		{"-df yes", false, false},
		{"-o /tmp/x", false, false},
		{"-w screen", false, false},
		{"-ac=opus", false, false},
		{"-ac", false, false},
		{"-ac -o", false, false},
		{`-ac ""`, false, false},
		{"opus -ac", false, false},
	}
	for _, tt := range tests {
		_, err := RecorderArgs(tt.in, tt.replay)
		if (err == nil) != tt.ok {
			t.Errorf("RecorderArgs(%q, %v) error = %v, want ok=%v", tt.in, tt.replay, err, tt.ok)
		}
	}
}
