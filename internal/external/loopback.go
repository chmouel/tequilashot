package external

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// vidiocQuerycap is VIDIOC_QUERYCAP, _IOR('V', 0, struct v4l2_capability).
const vidiocQuerycap = 0x80685600

// loopbackDriver is the driver name v4l2loopback reports.
const loopbackDriver = "v4l2 loopback"

// isLoopbackCapability reports whether a struct v4l2_capability, whose first
// 16 bytes are the NUL-padded driver name, belongs to v4l2loopback.
func isLoopbackCapability(capability []byte) bool {
	if len(capability) < 16 {
		return false
	}
	driver, _, _ := bytes.Cut(capability[:16], []byte{0})
	return string(driver) == loopbackDriver
}

// isLoopbackDevice asks the driver behind device for its name.
func isLoopbackDevice(device string) bool {
	f, err := os.OpenFile(device, os.O_RDWR|syscall.O_NONBLOCK, 0) //nolint:gosec // device comes from a /dev/video* glob
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var capability [104]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), vidiocQuerycap, uintptr(unsafe.Pointer(&capability[0]))) //nolint:gosec // VIDIOC_QUERYCAP fills this buffer
	return errno == 0 && isLoopbackCapability(capability[:])
}

// LoopbackDevices returns the v4l2loopback devices, lowest number first.
func LoopbackDevices() []string {
	matches, _ := filepath.Glob("/dev/video*")
	var devices []string
	for _, m := range matches {
		if ValidCameraDevice(m) && isLoopbackDevice(m) {
			devices = append(devices, m)
		}
	}
	slices.SortFunc(devices, func(a, b string) int {
		return videoNumber(a) - videoNumber(b)
	})
	return devices
}

func videoNumber(device string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(device, "/dev/video"))
	return n
}

// withoutDevices drops the modes of the given devices.
func withoutDevices(modes []CameraMode, devices []string) []CameraMode {
	if len(devices) == 0 {
		return modes
	}
	return slices.DeleteFunc(modes, func(m CameraMode) bool {
		return slices.Contains(devices, m.Device)
	})
}
