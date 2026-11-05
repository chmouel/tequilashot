<h1 align="center">
  <img src="assets/tequilashot-logo.svg" alt="tequilashot" width="420">
</h1>

<p align="center">Capture your Wayland desktop or record it with audio and webcam.</p>

<p align="center">
  <img src=".github/screenshots/social-preview.png" alt="Tequilashot capture dialogue with monitor previews, camera options and recording controls">
</p>

Tequilashot is a screenshot and screen-recording tool for Sway and Hyprland.
Use its commands from a key binding, or open the GTK/libadwaita dialogue to
choose a monitor, window or region.

## Features

- Capture a monitor, window or selected region to a file or clipboard.
- Record a monitor, window or region with GPU Screen Recorder.
- Add desktop audio, a microphone, or a webcam overlay to a recording.
- Edit screenshots, manage recording results and reuse settings in the capture
  dialogue.
- Read recording state from Waybar and control recordings with key bindings.

## Install

### Arch Linux

```sh
yay -S tequilashot-bin
```

### Prebuilt releases

Download a Linux archive or package from
[GitHub Releases](https://github.com/chmouel/tequilashot/releases). Releases
are published automatically when a `v*` tag is pushed. The first release under
the Tequilashot name will appear there after the next version tag. Archives are
available for x86_64 and aarch64, alongside deb, rpm and apk packages. Release
binaries are built without the GTK dialogue; the CLI capture and recording
commands are available.

### Build from source

Building the dialogue requires Go 1.25.6 or newer, a C toolchain, and the GTK 4
and libadwaita development files.

```sh
go install github.com/chmouel/tequilashot/cmd/tequilashot@main
```

Or build a local binary:

```sh
make build
bin/tequilashot
```

Install the runtime tools listed below for the features you use.

## Runtime tools

| Tool | Used for |
| --- | --- |
| [grim](https://sr.ht/~emersion/grim/) | Capturing Wayland output |
| [slurp](https://github.com/emersion/slurp) | Selecting a region |
| [gpu-screen-recorder](https://git.dec05eba.com/gpu-screen-recorder/about/) | Recording video, audio and webcam input |
| [wl-clipboard](https://github.com/bugaevc/wl-clipboard) | Clipboard commands |
| [FFmpeg](https://ffmpeg.org/) | Processing recordings and video previews |

[Satty](https://github.com/gabm/satty) opens screenshots for editing.
`aichat` can provide AI-generated filenames. A file manager and `fd` are used
for optional reveal and cleanup actions.

## Use the CLI

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
`large`). `--camera-mirror` flips the image. `movie-screen` also accepts
`--crop-top` to omit pixels from the top of the output.

## Capture dialogue

Run `tequilashot` from a source build to open the dialogue. Choose Screenshot
or Video, then select a monitor, window or region and set a countdown. Video
mode can include desktop audio, a microphone and a webcam overlay. The dialogue
remembers its choices between launches.

The dialogue is not included in the prebuilt release binaries. To build a CLI
binary without GTK, run:

```sh
CGO_ENABLED=0 go build -tags nogui -o bin/tequilashot ./cmd/tequilashot
```

## Configuration

The dialogue stores its settings at
`$XDG_CONFIG_HOME/tequilashot/tequilashot.json`, or
`~/.config/tequilashot/tequilashot.json` when `XDG_CONFIG_HOME` is unset.
Screenshots and recordings are saved to `~/Downloads/Screenshots` by default.

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `TEQUILASHOT_SAVE_LOCATION` | `~/Downloads/Screenshots` | Screenshot and recording directory |
| `TEQUILASHOT_AI_MODEL` | `gemini:gemini-2.5-flash-image` | Model used for AI-generated filenames |
| `TEQUILASHOT_WAYBAR_POLL_INTERVAL` | `1s` | Waybar status polling interval; values below `100ms` use `100ms` |

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
bindsym Print exec tequilashot selection-clipboard
bindsym Shift+Print exec tequilashot selection-file
bindsym $ctrl+Print exec tequilashot toggle-record -a movie-current-window -w 2
bindsym $ctrl+Shift+Print exec tequilashot stop-recording
```

## Development

```sh
make build
make test
```

`make screenshots` regenerates the dialogue and social-preview images. It
requires a Hyprland session, `grim`, FFmpeg, Pillow, Inter Display and the GTK
development files. The demo build stages sample windows and media; it does not
capture desktop content.

## Licence

Apache 2.0. See [LICENSE](LICENSE).
