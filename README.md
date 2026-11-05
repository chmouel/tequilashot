# sway-easyshot

A screenshot and screen recording utility for Sway/Wayland.

## Features

- Screenshot capture (window, screen, or selection)
- Screen recording with gpu-screen-recorder (hardware-accelerated, saved directly as MP4)
- Clipboard integration
- Image editing with satty
- Waybar status integration
- OBS integration
- Daemon mode.

## Dependencies

**Required:**

- [grim](https://sr.ht/~emersion/grim/) - screenshot capture
- [slurp](https://github.com/emersion/slurp) - region selection
- [gpu-screen-recorder](https://git.dec05eba.com/gpu-screen-recorder/about/) - screen recording
- [wl-clipboard](https://github.com/bugaevc/wl-clipboard) - clipboard (wl-copy/wl-paste)
- [ffmpeg](https://ffmpeg.org/) - remuxing OBS recordings to MP4
- [GTK 4](https://www.gtk.org/) and [libadwaita](https://gnome.pages.gitlab.gnome.org/libadwaita/) - runtime libraries for the `record-gui` dialogue (building from source requires their development headers and cgo)

**Optional:**

- [satty](https://github.com/gabm/satty) - screenshot annotation/editing
- [wofi](https://hg.sr.ht/~scoopta/wofi) - menu selection
- [zenity](https://gitlab.gnome.org/GNOME/zenity) - dialogs
- [nautilus](https://apps.gnome.org/Nautilus/) - file browser
- [fd](https://github.com/sharkdp/fd) - file cleanup
- [obs-cli](https://github.com/muesli/obs-cli) - OBS Studio control
- [pass](https://www.passwordstore.org/) - password store (for OBS)
- [aichat](https://github.com/sigoden/aichat) - AI-generated filenames

## Installation

## AUR Arch
```
yay install sway-easyshot-bin
```

### jsource
```bash
go install github.com/chmouel/sway-easyshot/cmd/sway-easyshot@latest
```
Or build directly:

```bash
make build
```

Prebuilt release binaries are static and do not include the `record-gui`
dialogue; the command there reports that it was built without GUI support.
Building from source with the GTK 4 and libadwaita development files installed
includes it. To build a static binary without the dialogue, use
`CGO_ENABLED=0 go build -tags nogui ./cmd/sway-easyshot`.

## Usage

```bash
# Screenshot commands
sway-easyshot selection-clipboard
sway-easyshot selection-file
sway-easyshot selection-edit
sway-easyshot current-window-clipboard
sway-easyshot current-window-file
sway-easyshot current-screen-clipboard

# Recording commands
sway-easyshot movie-selection
sway-easyshot movie-screen
sway-easyshot movie-screen --crop-top 75        # skip top 75px (e.g. status bar)
sway-easyshot movie-screen --current-screen --crop-top 75
sway-easyshot movie-current-window
sway-easyshot movie-screen --audio              # with desktop sound
sway-easyshot movie-screen --mic                # with the microphone
sway-easyshot movie-screen --audio --mic        # both, mixed into one track
sway-easyshot stop-recording
sway-easyshot pause-recording
sway-easyshot toggle-record
sway-easyshot toggle-record -a movie-screen --crop-top 75

# Recording dialogue (GTK4/libadwaita)
sway-easyshot record-gui

# Waybar integration
sway-easyshot waybar-status
sway-easyshot waybar-status --follow

# OBS integration
sway-easyshot obs-toggle-recording
sway-easyshot obs-toggle-pause
```

## Recording Dialogue

`sway-easyshot record-gui` opens a small libadwaita window in which to choose
the source (a monitor with a live thumbnail, an open window, or a region), the
audio sources (desktop sound, microphone) and a countdown of 0, 3, 5 or 10
seconds. Escape closes the window and Enter starts recording. The choices are
remembered in `$XDG_CONFIG_HOME/sway-easyshot/record-gui.json`
(`~/.config` when unset).

The window hides whilst recording and returns, showing a result card, once the
recording ends: a frame from the video, its name, duration, resolution and size,
with buttons to play it, reveal it in the file manager, copy its path, rename it
or move it to the bin. "New recording" (or Escape) returns to the setup page.
Previews and details rely on `ffmpeg` and `ffprobe`. Running `record-gui` whilst a recording is in progress
stops it and shows the window again, so a single key binding suffices. If the
key is pressed whilst the recording is still being set up (countdown or region
selection), the stop is held and applied as soon as recording begins. Monitor
thumbnails refresh every two seconds whilst the window is visible.
Window and monitor listing supports both Hyprland and sway.

The dialogue drives the daemon through two additions to the request options,
which other clients may use as well:

- `movie-screen` accepts an `output` option naming the output to record,
  bypassing the interactive selection when present.
- `movie-geometry` is a new action recording the `geometry` option, given in
  slurp form (`"X,Y WxH"`). It honours `delay`, `audio` and `mic`.
- `quiet_finish` (boolean, on any recording start) tells the daemon that the
  client presents the result itself. When such a recording stops, the daemon
  sends only a brief "Recording saved" notification, without the copy, show,
  rename and delete actions, and does not wait for a choice. The flag lasts for
  that recording only; recordings started by other means keep the actions.

## Waybar Configuration

```json
"custom/screenshot": {
    "exec": "sway-easyshot waybar-status --follow",
    "on-click": "sway-easyshot toggle-record -a movie-current-window",
    "return-type": "json"
}
```

## Sway Configuration

```ini
bindsym Print exec sway-easyshot selection-clipboard
bindsym Shift+Print exec sway-easyshot selection-file
bindsym $alt+Print exec sway-easyshot selection-edit
bindsym $super+Print exec sway-easyshot current-window-clipboard
bindsym $super+Shift+Print exec sway-easyshot current-window-file
bindsym $ctrl+Print exec sway-easyshot toggle-record -a movie-current-window -w 2
bindsym $ctrl+Shift+Print exec sway-easyshot stop-recording
```

bindsym Print exec sway-easyshot toggle-record -a movie-current-window -w 5

## Licence

Apache 2.0
