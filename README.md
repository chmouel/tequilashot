<h1 align="center">
  <img src="assets/tequilashot-logo.svg" alt="tequilashot" width="420">
</h1>

<p align="center">Screenshots and screen recordings for Sway and Hyprland, from a small GTK dialogue or the command line.</p>

<p align="center">
  <img src=".github/screenshots/social-preview.png" alt="Tequilashot capture dialogue with monitor previews, camera options and recording controls">
</p>

Tequilashot captures a monitor, a window or a region you draw, as an image or
as a video with desktop sound, your microphone and your webcam. Run
`tequilashot` without arguments to open the capture dialogue. Everything the
dialogue does is also available as a command for key bindings and Waybar.

## The capture dialogue

Run `tequilashot`. The dialogue opens as a compact floating window, starts the
background daemon if it is not already running, and remembers your choices for
the next launch. Press Enter to start a capture and Escape to close it.

### Choose what to capture

<p align="center">
  <img src=".github/screenshots/dialogue-screenshot.png" alt="Screenshot mode with monitor thumbnails and a countdown" width="45%">
  &nbsp;
  <img src=".github/screenshots/dialogue-window.png" alt="Window list with a preview of each open window" width="45%">
</p>

Pick Screenshot or Video at the top, then a source:

- Monitor shows a thumbnail of each output, kept up to date while the dialogue
  is open. Click the one you want.
- Window lists the open windows with a preview, their application and size.
- Region lets you drag out an area once you press Capture or Record.

Set a countdown of 0, 3, 5 or 10 seconds. The dialogue steps out of the way
before anything is captured, so it never appears in your shot.

### Record with sound and camera

<p align="center">
  <img src=".github/screenshots/dialogue-monitor.png" alt="Video mode with desktop sound, microphone and the camera panel open" width="80%">
</p>

Video mode adds three toggles. Desktop sound records what your speakers play.
Microphone records your voice, and the row beneath it chooses the input
device. Camera opens a panel beside the dialogue with a live preview of your
webcam laid over the selected monitor or window, at the size and corner it
will have in the video. The Camera toggle appears only when a webcam is found.

In the camera panel you can:

- click a corner of the preview, or use the arrow keys, to move the camera;
- choose Small, Medium or Large;
- pick a Square or Circle shape;
- mirror the picture horizontally.

For a monitor recording, Skip top bar trims a number of pixels from the top of
the output, which keeps a panel or bar out of the video.

Press Record and the dialogue hides while the countdown runs. To stop, run
`tequilashot` again (a key binding is the easiest way), run
`tequilashot stop-recording`, or click the Waybar module. The dialogue then
returns with the finished recording.

A window recording captures the area the window occupies when you press
Record. If you move the window afterwards, the recording stays where it was.

### Round camera

<p align="center">
  <img src=".github/screenshots/dialogue-camera-circle.png" alt="Camera panel with the Circle shape selected and a round webcam in the preview" width="80%">
</p>

The square camera is added to the video by GPU Screen Recorder and you do not
see it on screen while you record. The Circle shape works differently: for the
length of the recording, Tequilashot shows your webcam as a round bubble with a
thin white ring above your other windows, and the recording picks it up from
the screen. You see exactly what your viewers will see.

You can drag the bubble anywhere on its monitor during the recording; keep it
inside the recorded area if you want it in the video. Clicks around the bubble
reach the windows beneath it. The bubble closes when the recording stops.

The round camera needs
[gtk4-layer-shell](https://github.com/wmww/gtk4-layer-shell), a compositor with
layer-shell support (Sway and Hyprland both have it), and a build that includes
the dialogue. It stays on the monitor where it started and does not follow a
recorded window that moves.

### After the capture

<p align="center">
  <img src=".github/screenshots/dialogue-result.png" alt="Recording saved page with playback, folder, rename and delete actions" width="45%">
  &nbsp;
  <img src=".github/screenshots/dialogue-screenshot-result.png" alt="Screenshot saved page with copy, edit and open actions" width="45%">
</p>

The result page shows a preview of the file with its name and details: length,
resolution and size for a video, dimensions and size for an image. From there
you can play or open it, show it in its folder, copy its path, rename it (the
extension is kept) or move it to the bin. Screenshots also offer Copy image and
Edit, which opens the image in [Satty](https://github.com/gabm/satty) and
refreshes the preview when you save. New recording or New screenshot, or
Escape, takes you back to the setup page.

## Install

The capture dialogue needs a build from source. The prebuilt release binaries
and the AUR package contain the command-line tools only.

### Build from source

You need Go 1.25.6 or newer, a C toolchain, and the GTK 4 and libadwaita
development files.

```sh
go install github.com/chmouel/tequilashot/cmd/tequilashot@main
```

Or build a local binary:

```sh
make build
bin/tequilashot
```

To build the command-line tools without GTK:

```sh
CGO_ENABLED=0 go build -tags nogui -o bin/tequilashot ./cmd/tequilashot
```

### Prebuilt releases

Arch Linux users can install the command-line build from the AUR:

```sh
yay -S tequilashot-bin
```

Linux archives for x86_64 and aarch64, along with deb, rpm and apk packages, are
published on [GitHub Releases](https://github.com/chmouel/tequilashot/releases)
whenever a `v*` tag is pushed. The first release under the Tequilashot name
will appear after the next version tag.

### Runtime tools

| Tool | Used for |
| --- | --- |
| [grim](https://sr.ht/~emersion/grim/) | Capturing Wayland output |
| [slurp](https://github.com/emersion/slurp) | Selecting a region |
| [gpu-screen-recorder](https://git.dec05eba.com/gpu-screen-recorder/about/) | Recording video, audio and webcam input |
| [wl-clipboard](https://github.com/bugaevc/wl-clipboard) | Clipboard commands |
| [FFmpeg](https://ffmpeg.org/) | Camera previews, the round camera and recording details |

Optional tools:
[gtk4-layer-shell](https://github.com/wmww/gtk4-layer-shell) for the round
camera, [Satty](https://github.com/gabm/satty) for editing screenshots,
[aichat](https://github.com/sigoden/aichat) for AI-generated file names, and
[fd](https://github.com/sharkdp/fd) for the daemon's clean-up of old files (see
[Configuration](#configuration)).

## Command line

Each capture is also a command, which suits key bindings and scripts. The
commands talk to the same daemon as the dialogue and start it when needed.

```sh
# Capture a selected region
tequilashot selection-clipboard
tequilashot selection-file
tequilashot selection-edit

# Capture the focused window or screen
tequilashot current-window-clipboard
tequilashot current-window-file
tequilashot current-screen-clipboard

# Record a selection, screen or focused window
tequilashot movie-selection
tequilashot movie-screen --audio --mic
tequilashot movie-current-window --camera

# Control a recording
tequilashot pause-recording
tequilashot stop-recording
tequilashot toggle-record -a movie-current-window -w 2
```

Recording commands accept `--audio` for desktop sound and `--mic` for the
default microphone. Use `--mic-device` with an id printed by
`gpu-screen-recorder --list-audio-devices` to select an input; naming a device
also enables the microphone.

Add a webcam with `--camera` or `-C`. Choose a device with `--camera-device`,
position it with `--camera-position` (`top-left`, `top-right`, `bottom-left`
or `bottom-right`), and set its size with `--camera-size` (`small`, `medium` or
`large`). `--camera-mirror` flips the image, and `--camera-shape circle` shows
the [round camera](#round-camera) instead of the square one. `movie-screen`
also accepts `--crop-top` to omit pixels from the top of the output.

## Configuration

The dialogue stores its settings at
`$XDG_CONFIG_HOME/tequilashot/tequilashot.json`, or
`~/.config/tequilashot/tequilashot.json` when `XDG_CONFIG_HOME` is unset.

Screenshots and recordings are saved to `~/Downloads/Screenshots` by default.
Treat this folder as a scratch area: when `fd` is installed, the daemon deletes
files in it that are older than three days, once at start-up and then daily.
Move anything you want to keep elsewhere, or point the save location at a
dedicated folder.

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `TEQUILASHOT_SAVE_LOCATION` | `~/Downloads/Screenshots` | Screenshot and recording directory |
| `TEQUILASHOT_AI_MODEL` | `gemini:gemini-2.5-flash-image` | Model used for AI-generated filenames |
| `TEQUILASHOT_WAYBAR_POLL_INTERVAL` | `1s` | Waybar status polling interval; values below `100ms` use `100ms` |

The daemon starts on demand. To run it with your session instead, install
[`systemd/tequilashot.service`](systemd/tequilashot.service) as a user unit and
adjust its `ExecStart` path to where the binary lives.

## Waybar

```json
"custom/recording": {
  "exec": "tequilashot waybar-status --follow",
  "on-click": "tequilashot toggle-record -a movie-current-window",
  "return-type": "json"
}
```

## Sway key bindings

```ini
bindsym $mod+Print exec tequilashot
bindsym Print exec tequilashot selection-clipboard
bindsym Shift+Print exec tequilashot selection-file
bindsym $ctrl+Print exec tequilashot toggle-record -a movie-current-window -w 2
bindsym $ctrl+Shift+Print exec tequilashot stop-recording
```

The first binding opens the dialogue, and pressing it again during a
recording started from the dialogue stops it.

## Development

See [DEVELOPER.md](DEVELOPER.md) for building, testing, regenerating the
screenshots, releasing and the daemon socket protocol.

Apache 2.0. See [LICENSE](LICENSE).
