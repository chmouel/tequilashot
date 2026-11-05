# sway-easyshot

Screenshots and screen recordings for sway and Hyprland.

![The capture dialogue: video setup with the webcam overlay, the window picker, and the result cards for a recording and a screenshot](.github/screenshots/dialogue.png)

## Features

- Screenshots of a window, a screen or a selected region
- Hardware-accelerated screen recording with gpu-screen-recorder, saved as MP4
- Optional webcam overlay on recordings
- A GTK 4 capture dialogue for both screenshots and recordings
- Clipboard support and image editing with Satty
- A Waybar status module
- A background daemon, started on demand

## Dependencies

**Required:**

- [grim](https://sr.ht/~emersion/grim/) - screenshot capture
- [slurp](https://github.com/emersion/slurp) - region selection
- [gpu-screen-recorder](https://git.dec05eba.com/gpu-screen-recorder/about/) - screen recording
- [wl-clipboard](https://github.com/bugaevc/wl-clipboard) - clipboard (wl-copy/wl-paste)
- [ffmpeg](https://ffmpeg.org/) - webcam preview and recording details in the capture dialogue
- [GTK 4](https://www.gtk.org/) and [libadwaita](https://gnome.pages.gitlab.gnome.org/libadwaita/) - runtime libraries for the capture dialogue (building from source requires their development headers and cgo)

**Optional:**

- [satty](https://github.com/gabm/satty) - screenshot annotation/editing
- [wofi](https://hg.sr.ht/~scoopta/wofi) - menu selection
- [zenity](https://gitlab.gnome.org/GNOME/zenity) - dialogs
- [nautilus](https://apps.gnome.org/Nautilus/) - file browser
- [fd](https://github.com/sharkdp/fd) - file cleanup
- [aichat](https://github.com/sigoden/aichat) - AI-generated filenames

## Installation

### Arch Linux (AUR)

```bash
yay -S sway-easyshot-bin
```

### From source

```bash
go install github.com/chmouel/sway-easyshot/cmd/sway-easyshot@latest
```

Or build directly:

```bash
make build
```

The prebuilt release binaries are static and leave out the capture dialogue;
running `sway-easyshot` without a command on one of them reports that it was
built without GUI support. A build from source includes the dialogue when the
GTK 4 and libadwaita development files are installed. To build a static binary
without it, use `CGO_ENABLED=0 go build -tags nogui ./cmd/sway-easyshot`.

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
sway-easyshot movie-screen --mic-device alsa_input.usb-Razer_Seiren-00.mono-fallback
sway-easyshot movie-screen --camera             # webcam in the bottom-right corner
sway-easyshot movie-selection -C --camera-position top-left --camera-size large --camera-mirror
sway-easyshot movie-screen -C --camera-device /dev/video2
sway-easyshot stop-recording
sway-easyshot pause-recording
sway-easyshot toggle-record
sway-easyshot toggle-record -a movie-screen --crop-top 75

# Capture dialogue (GTK 4/libadwaita), the default without a command
sway-easyshot

# Waybar integration
sway-easyshot waybar-status
sway-easyshot waybar-status --follow
```

## Microphone Selection

By default `--mic` records the system's default input. `--mic-device` picks a
particular microphone by the id that `gpu-screen-recorder --list-audio-devices`
prints before the `|`, and switches the microphone on even without `--mic`.
`movie-selection`, `movie-screen`, `movie-current-window` and `toggle-record`
accept the flag. If the device is not present, the command fails before the
countdown begins.

## Webcam Overlay

`movie-selection`, `movie-screen`, `movie-current-window` and `toggle-record`
accept these flags:

- `--camera`, `-C` overlays the webcam on the recording.
- `--camera-device` names the device, such as `/dev/video0`. The default is the
  first camera listed by `gpu-screen-recorder --list-v4l2-devices`.
- `--camera-position` is `top-left`, `top-right`, `bottom-left` or
  `bottom-right` (the default).
- `--camera-size` is `small` (15 % of the video width), `medium` (25 %, the
  default) or `large` (35 %).
- `--camera-mirror` flips the image horizontally.

`gpu-screen-recorder` composites the camera itself, so no further dependency is
needed. Most webcams serve one application at a time; if another program holds
the camera, the recording fails to start and says why. Start requests wait as
long as the selection and countdown take.

## Capture Dialogue

Running `sway-easyshot` without a command opens a small libadwaita window. You
pick Screenshot or Video, then a source (a monitor, an open window or a region)
and a countdown of 0, 3, 5 or 10 seconds. Enter captures, Escape closes the
window. The dialogue remembers your choices in
`$XDG_CONFIG_HOME/sway-easyshot/sway-easyshot.json` (`~/.config` when unset)
and opens in Video mode the first time.

Monitor and window entries show thumbnails that refresh every two seconds. A
window thumbnail shows that area of the screen as it currently is, overlapping
windows included. The list holds visible windows only: the dialogue itself and
the hidden tabs of a tabbed group are left out, and the list follows tab
changes. Both Hyprland and sway are supported.

### Screenshots

Capture hides the dialogue and saves a PNG in `SWAY_SCREENSHOT_SAVE_LOCATION`
(`~/Downloads/Screenshots` by default). The result card then shows the image
with its name, resolution and size. From there you can open it, copy the image
or its path, edit it in Satty, show it in the file manager, rename it or move
it to the bin. Satty saves over the same file, and the preview refreshes when
Satty closes. "New screenshot" or Escape returns to the setup page. AI naming
is only offered through the actions on the CLI notifications.

### Recordings

Video mode adds desktop sound and microphone switches, a microphone selector
(the system default or any detected input), the webcam overlay and, for a
monitor, a crop of the top bar. The Camera button appears only when a webcam is
found. Switching it on shows a live preview with the camera picture placed over
the chosen source exactly as it will be recorded. Click near a corner of the
preview, or use the arrow keys, to move the camera there, and pick the device,
the size (small, medium or large) and whether to mirror it. The camera is in
use only whilst the setup page is showing and is released before recording
begins.

The window hides during the recording and comes back with a result card when it
ends: a frame from the video, its name, duration, resolution and size, and
buttons to play it, show it in the file manager, copy its path, rename it or
move it to the bin. "New recording" or Escape returns to the setup page. The
frame and details come from `ffmpeg` and `ffprobe`.

Running `sway-easyshot` again during a recording stops it and brings the window
back, so one key binding covers both. A stop requested during the countdown or
region selection takes effect as soon as recording starts.

### Daemon requests

The dialogue talks to the daemon with these requests and options, which other
clients may use too:

- `screenshot` saves a PNG and returns its path in the response's `file` field.
  It takes `delay` and either `output` (a monitor name) or `geometry` (in slurp
  form); with neither, it asks for a region. It returns without waiting for
  notification actions. The screenshot CLI commands do not use it and behave
  as before.
- `movie-screen` records the monitor named by the `output` option, when given,
  instead of asking for one.
- `movie-geometry` records the `geometry` option, given in slurp form
  (`"X,Y WxH"`). It honours `delay`, `audio` and `mic`.
- `mic_device` (string, on any recording start) names the microphone to use
  when `mic` is set, with the same ids as `--mic-device`. An empty value means
  the default input; an unknown device is refused before anything starts.
- `camera` (boolean, on any recording start) overlays the webcam. The optional
  `camera_device`, `camera_position`, `camera_size` and `camera_mirror` take
  the values of the matching command-line flags. Invalid values are refused
  before anything starts.
- `quiet_finish` (boolean, on any recording start) marks a recording whose
  client shows the result itself. When it stops, the daemon sends a plain
  "Recording saved" notification with no copy, show, rename or delete actions
  and does not wait for a choice. The flag covers that recording only.

### README images

`make screenshots` renders the images in this README from a `demo` build with
staged monitors, windows, a screenshot and a recording, so no real desktop
content is captured. It needs a Hyprland session, `grim`, `ffmpeg` and Pillow.
Leave the desktop alone whilst it runs; the script stops if anything covers
the dialogue.

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

To open the capture dialogue with a key instead:

```ini
bindsym Print exec sway-easyshot
```

## Licence

Apache 2.0
