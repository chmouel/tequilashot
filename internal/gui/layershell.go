//go:build !nogui

package gui

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdint.h>

static void *layer_lib;

static const char *layer_open(void) {
	layer_lib = dlopen("libgtk4-layer-shell.so.0", RTLD_NOW | RTLD_GLOBAL);
	return layer_lib ? NULL : dlerror();
}

static int layer_supported(void) {
	int (*fn)(void) = (int (*)(void))dlsym(layer_lib, "gtk_layer_is_supported");
	return fn && fn();
}

// layer_place turns window into an overlay layer surface on monitor, anchored
// to its top-left corner at the given margins, ignoring other exclusive zones
// and never taking the keyboard.
static const char *layer_place(uintptr_t window, uintptr_t monitor, int left, int top) {
	void (*init)(void *) = dlsym(layer_lib, "gtk_layer_init_for_window");
	void (*set_namespace)(void *, const char *) = dlsym(layer_lib, "gtk_layer_set_namespace");
	void (*set_layer)(void *, int) = dlsym(layer_lib, "gtk_layer_set_layer");
	void (*set_monitor)(void *, void *) = dlsym(layer_lib, "gtk_layer_set_monitor");
	void (*set_anchor)(void *, int, int) = dlsym(layer_lib, "gtk_layer_set_anchor");
	void (*set_margin)(void *, int, int) = dlsym(layer_lib, "gtk_layer_set_margin");
	void (*set_zone)(void *, int) = dlsym(layer_lib, "gtk_layer_set_exclusive_zone");
	void (*set_keyboard)(void *, int) = dlsym(layer_lib, "gtk_layer_set_keyboard_mode");
	if (!init || !set_namespace || !set_layer || !set_monitor || !set_anchor || !set_margin || !set_zone || !set_keyboard) {
		return "gtk4-layer-shell is missing expected functions";
	}

	void *win = (void *)window;
	enum { LAYER_OVERLAY = 3, EDGE_LEFT = 0, EDGE_TOP = 2, KEYBOARD_NONE = 0 };
	init(win);
	set_namespace(win, "tequilashot-camera");
	set_layer(win, LAYER_OVERLAY);
	if (monitor) {
		set_monitor(win, (void *)monitor);
	}
	set_anchor(win, EDGE_LEFT, 1);
	set_anchor(win, EDGE_TOP, 1);
	set_margin(win, EDGE_LEFT, left);
	set_margin(win, EDGE_TOP, top);
	set_zone(win, -1);
	set_keyboard(win, KEYBOARD_NONE);
	return NULL;
}
*/
import "C"

import (
	"errors"
	"fmt"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// loadLayerShell loads gtk4-layer-shell at run time so that only the round
// camera depends on it. It must already be preloaded for it to work.
func loadLayerShell() error {
	if msg := C.layer_open(); msg != nil {
		return fmt.Errorf("the round camera needs gtk4-layer-shell: %s", C.GoString(msg))
	}
	return nil
}

func layerShellSupported() bool {
	return C.layer_supported() != 0
}

// placeLayer makes win an overlay on monitor at the given offset from its
// top-left corner. It must be called before the window is shown.
func placeLayer(win *gtk.Window, monitor *gdk.Monitor, left, top int) error {
	var mon uintptr
	if monitor != nil {
		mon = coreglib.BaseObject(monitor).Native()
	}
	if msg := C.layer_place(C.uintptr_t(coreglib.BaseObject(win).Native()), C.uintptr_t(mon), C.int(left), C.int(top)); msg != nil {
		return errors.New(C.GoString(msg))
	}
	return nil
}
