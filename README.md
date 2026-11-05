<h1 align="center">
  <img src="assets/tequilashot-logo.svg" alt="tequilashot" width="420">
</h1>

<p align="center">Screenshots and screen recordings for Sway and Hyprland, from a small GTK dialogue or the command line.</p>

<p align="center">
  <img src=".github/screenshots/social-preview.png" alt="Tequilashot capture dialogue with monitor previews, camera options and recording controls">
</p>

Run `tequilashot` with no arguments to open the capture dialogue. It starts
the daemon if needed and remembers your last settings. Enter captures, Escape
closes. Every capture is also a command, for key bindings and Waybar.

## The capture dialogue

### Choose what to capture

<p align="center">
  <img src=".github/screenshots/dialogue-screenshot.png" alt="Screenshot mode with monitor thumbnails and a countdown" width="45%">
  &nbsp;
  <img src=".github/screenshots/dialogue-window.png" alt="Window list with a preview of each open window" width="45%">
</p>

Pick Screenshot or Video, then a source:

- Monitor: live thumbnails of each output.
- Window: open windows with a preview, application and size.
- Region: drag out an area after pressing Capture or Record.

Countdown is 0, 3, 5 or 10 seconds. The dialogue hides before capturing.

### Record with sound and camera

<p align="center">
  <img src=".github/screenshots/dialogue-monitor.png" alt="Video mode with desktop sound, microphone and the camera panel open" width="80%">
</p>

Video mode adds:

- Desktop sound.
- Microphone, with an input device picker.
- Camera, shown only when a webcam is found. It opens a panel with a live
  preview over the selected source. Click a corner or use the arrow keys to
  place it, and choose Small, Medium or Large, Square or Circle, and mirroring.
- Skip top bar, for monitor recordings: trims pixels from the top of the
  output to hide a bar.

The dialogue hides during the recording. Stop it by running `tequilashot`
again, `tequilashot stop-recording`, or clicking the Waybar module; the
dialogue then shows the result.

A window recording captures the window's area at the time you press Record.
Moving the window afterwards does not move the recorded area.

### Round camera

<p align="center">
  <img src=".github/screenshots/dialogue-camera-circle.png" alt="Camera panel with the Circle shape selected and a round webcam in the preview" width="80%">
</p>

GPU Screen Recorder adds the square camera to the video file, so it is not
visible on screen. The Circle shape instead shows the webcam on screen as a
round bubble with a white ring, above other windows, and the recording
captures it from there.

You can drag the bubble around its monitor while recording; keep it inside the
recorded area if you want it in the video. Clicks outside the circle go
through to the windows beneath. The bubble closes when the recording stops
and does not follow a moving window.

It needs a GUI build, [gtk4-layer-shell](https://github.com/wmww/gtk4-layer-shell)
and a layer-shell compositor such as Sway or Hyprland.

### After the capture

<p align="center">
  <img src=".github/screenshots/dialogue-result.png" alt="Recording saved page with playback, folder, rename and delete actions" width="45%">
  &nbsp;
  <img src=".github/screenshots/dialogue-screenshot-result.png" alt="Screenshot saved page with copy, edit and open actions" width="45%">
</p>

The result page shows a preview with the file's size and its length,
resolution or dimensions. You can play or open it, show it in its folder, copy
its path, rename it (keeping the extension) or move it to the bin. Screenshots
add Copy image and Edit, which opens [Satty](https://github.com/gabm/satty)
and reloads the preview on save. New recording, New screenshot or Escape goes
back to the setup page.

## Install

The capture dialogue needs a build from source. Release binaries and the AUR
package are command line only.

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

[GitHub Releases](https://github.com/chmouel/tequilashot/releases) has Linux
archives for x86_64 and aarch64, plus deb, rpm and apk packages.

### Runtime tools

| Tool | Used for |
| --- | --- |
| [grim](https://sr.ht/~emersion/grim/) | Capturing Wayland output |
| [slurp](https://github.com/emersion/slurp) | Selecting a region |
| [gpu-screen-recorder](https://git.dec05eba.com/gpu-screen-recorder/about/) | Recording video, audio and webcam input |
| [wl-clipboard](https://github.com/bugaevc/wl-clipboard) | Clipboard commands |
| [FFmpeg](https://ffmpeg.org/) | Camera previews, the round camera and recording details |

Optional: [gtk4-layer-shell](https://github.com/wmww/gtk4-layer-shell) (round
camera), [Satty](https://github.com/gabm/satty) (editing screenshots),
[aichat](https://github.com/sigoden/aichat) (AI-generated file names).

## Command line

The commands use the same daemon as the dialogue and start it when needed.

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

Recording flags:

- `--audio`: desktop sound.
- `--mic`: default microphone. `--mic-device <id>` picks one from
  `gpu-screen-recorder --list-audio-devices` and implies `--mic`.
- `--camera`, `-C`: webcam. Tune it with `--camera-device`,
  `--camera-position` (`top-left`, `top-right`, `bottom-left`, `bottom-right`),
  `--camera-size` (`small`, `medium`, `large`), `--camera-mirror` and
  `--camera-shape circle` for the [round camera](#round-camera).
- `--crop-top <px>` (screen recordings only): trim pixels from the top.

## Configuration

The dialogue stores its settings at
`$XDG_CONFIG_HOME/tequilashot/tequilashot.json`, or
`~/.config/tequilashot/tequilashot.json` when `XDG_CONFIG_HOME` is unset.

Captures go to `~/Downloads/Screenshots` by default. The daemon deletes files
directly in that folder once they are older than three days, at start-up and
then daily. Subfolders, hidden files and symlinks are kept, so move anything
you want to keep, or set a dedicated folder.

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `TEQUILASHOT_SAVE_LOCATION` | `~/Downloads/Screenshots` | Screenshot and recording directory |
| `TEQUILASHOT_AI_MODEL` | `gemini:gemini-2.5-flash-image` | Model used for AI-generated filenames |
| `TEQUILASHOT_WAYBAR_POLL_INTERVAL` | `1s` | Waybar status polling interval; values below `100ms` use `100ms` |

The daemon starts on demand. To start it with your session, install
[`systemd/tequilashot.service`](systemd/tequilashot.service) as a user unit and
fix its `ExecStart` path.

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

`$mod+Print` opens the dialogue, or stops a recording started from it.

## Development

See [DEVELOPER.md](DEVELOPER.md) for tests, screenshots, releases and the
daemon socket protocol.

Apache 2.0. See [LICENSE](LICENSE).
