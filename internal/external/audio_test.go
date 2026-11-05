package external

import (
	"strings"
	"testing"
)

func TestParseMicrophones(t *testing.T) {
	listing := `default_output|Default output
default_input|Default input
alsa_output.pci-0000_c4_00.6.pro-output-0.monitor|Monitor of Ryzen
alsa_input.pci-0000_c4_00.6.pro-input-0|Ryzen HD Audio Controller Pro
garbage
|no id
noname|
alsa_input.usb-Razer-00.mono-fallback|Razer Seiren V2 Pro Mono
`
	got := ParseMicrophones(listing)
	want := []AudioDevice{
		{"alsa_input.pci-0000_c4_00.6.pro-input-0", "Ryzen HD Audio Controller Pro"},
		{"alsa_input.usb-Razer-00.mono-fallback", "Razer Seiren V2 Pro Mono"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("device %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestValidateMicDevice(t *testing.T) {
	for _, ok := range []string{"", "alsa_input.usb-Razer_Inc-00.mono-fallback", "a:b@c+d"} {
		if err := ValidateMicDevice(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"a|b", "a;b", "a b", "a\nb", strings.Repeat("a", maxMicDeviceLen+1)} {
		if err := ValidateMicDevice(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestGsrSourceMicDevice(t *testing.T) {
	cases := []struct {
		a    AudioSources
		want string
	}{
		{AudioSources{Mic: true}, "default_input"},
		{AudioSources{Mic: true, MicDevice: "alsa_input.x"}, "device:alsa_input.x"},
		{AudioSources{Desktop: true, Mic: true, MicDevice: "alsa_input.x"}, "default_output|device:alsa_input.x"},
		{AudioSources{MicDevice: "alsa_input.x"}, ""},
	}
	for _, c := range cases {
		if got := c.a.gsrSource(); got != c.want {
			t.Errorf("%+v = %q, want %q", c.a, got, c.want)
		}
	}
	if _, err := gsrArgs("", "DP-1", "o.mp4", 0, 0, AudioSources{Mic: true, MicDevice: "x|y"}, CameraOverlay{}); err == nil {
		t.Error("injected mic device accepted")
	}
}
