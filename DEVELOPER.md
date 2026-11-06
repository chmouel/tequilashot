# Developing Tequilashot

## Building and checking

```sh
make build    # bin/tequilashot, including the capture dialogue
make test     # go test ./...
make lint     # golangci-lint run --fix ./...
make format   # gofumpt -w .
make sanity   # lint, format and test
```

The default build links GTK 4, libadwaita and, at run time, gtk4-layer-shell
for the round camera. Two build tags change what is compiled:

| Tag | Effect |
| --- | --- |
| `nogui` | Leaves out the dialogue and the round camera; builds with `CGO_ENABLED=0`. The aarch64 tarball and the apk packages use it. |
| `demo` | Replaces monitors, windows, cameras and microphones with staged content and disables capture. Used for the README screenshots. |

The code is laid out as follows: `cmd/tequilashot` holds the CLI and its
commands, `internal/daemon` serves the socket, `internal/commands` runs
captures and recordings, `internal/external` wraps grim, slurp,
gpu-screen-recorder, FFmpeg and the other helpers, `internal/gui` is the
dialogue and round camera, and `pkg/protocol` defines the socket messages.

## Screenshots

`make screenshots` rebuilds the dialogue and social-preview images in
`.github/screenshots`. It needs a Hyprland session, `grim`, FFmpeg, Pillow, the
Inter Display font and the GTK development files. The script builds the `demo`
binary, opens each page in turn and captures it. The demo stages sample
windows, a webcam silhouette and media files, so no real desktop content is
recorded.

Leave the desktop alone while it runs. If another floating window overlaps the
dialogue, or its workspace is not visible, the script stops without writing any
image. Run `make optimize` afterwards to compress the PNG files with
`pngquant`.

To look at one staged page by hand, build the demo binary and set
`TEQUILASHOT_DEMO` to a page name (`monitor`, `camera-circle`, `window`,
`result`, `screenshot` or `screenshot-result`) and `TEQUILASHOT_DEMO_DIR` to a
directory holding the staged assets that `hack/screenshots.py` writes.

## Releases

Run `make release` (or `scripts/make-release.sh --patch`, `--minor`,
`--major`; add `-n` for a dry run) to create and push a signed `vX.Y.Z` tag.
Pushing a `v*` tag starts the release workflow, which runs GoReleaser inside an
Arch Linux container: it builds a GTK 4 binary for x86_64 and a `nogui` binary
for x86_64 and aarch64, packs the desktop entry and the icon where the dialogue
is included, writes release notes grouped by Conventional Commit type, and
publishes `tequilashot-bin` to the AUR. Once the release is published, the
workflow rewrites its notes from the commit range with Gemini.

That last step needs a `GEMINI_API_KEY` repository secret. Without it the
workflow logs a notice and keeps the changelog GoReleaser wrote, so the release
still succeeds.

## Daemon socket protocol

The CLI and the dialogue both send their work to a daemon, which they start on
demand with `tequilashot daemon` (add `--debug` for more logging). The daemon
listens on a Unix socket at `/run/user/$UID/tequilashot.sock`, created with mode
`0600`.

Each connection carries one JSON request and one JSON response:

```json
{"command": "execute", "action": "movie-screen", "options": {"audio": true, "delay": 3}}
```

The daemon dispatches on `action`; `command` is sent as `"execute"` by the
built-in clients. `options` may be omitted.

```sh
printf '{"command":"execute","action":"waybar-status"}\n' |
  socat - "UNIX-CONNECT:/run/user/$(id -u)/tequilashot.sock"
```

### Actions

| Action | What it does |
| --- | --- |
| `screenshot` | Captures `output` or `geometry` (or a slurp selection when neither is given) to a file and returns its path in `file`. |
| `selection-clipboard`, `selection-file`, `selection-edit` | Captures a slurp selection to the clipboard, a file, or Satty. |
| `current-window-clipboard`, `current-window-file` | Captures the focused window. |
| `current-screen-clipboard` | Captures the current screen to the clipboard. |
| `movie-selection` | Records a slurp selection. |
| `movie-screen` | Records `output` when given, otherwise the focused output with `use_current_screen` or one picked interactively. |
| `movie-geometry` | Records the `geometry` area. |
| `movie-current-window` | Records the focused window's area. |
| `pause-recording` | Pauses the current recording, or resumes it when paused. |
| `stop-recording` | Stops the current recording. |
| `toggle-record` | Stops the current recording, or starts `start_action` (default `movie-selection`). |
| `waybar-status` | Returns the Waybar status as a JSON string in `message`. |

Recording actions reply once the recorder is running, not when the recording
ends. Watch `state.recording` with `waybar-status` to learn when it stops.

### Options

| Option | Type | Used by |
| --- | --- | --- |
| `delay` | number (seconds) | Captures and recordings |
| `output` | string, an output name | `screenshot`, `movie-screen` |
| `geometry` | string, `"X,Y WxH"` | `screenshot`, `movie-geometry` |
| `use_current_screen` | boolean | `current-screen-clipboard`, `movie-screen`, `toggle-record` |
| `crop_top` | number (pixels) | `movie-screen`, `toggle-record` |
| `audio`, `mic` | boolean | Recordings |
| `mic_device` | string, a gpu-screen-recorder audio device id | Recordings |
| `camera` | boolean, enables the `camera_*` options | Recordings |
| `camera_device` | string, `/dev/videoN` | Recordings |
| `camera_position` | `top-left`, `top-right`, `bottom-left`, `bottom-right` | Recordings |
| `camera_size` | `small`, `medium`, `large` | Recordings |
| `camera_shape` | `square`, `circle` | Recordings |
| `camera_mirror` | boolean | Recordings |
| `quiet_finish` | boolean; the finish notification omits its file actions because the client shows the result | Recordings |
| `start_action` | string, a recording action | `toggle-record` |
| `icons` | object with `Idle`, `Recording`, `Paused` and `Countdown` strings | `waybar-status` |

Options of the wrong type are rejected. Camera values are validated when the
recording starts.

### Response

```json
{
  "success": true,
  "message": "Command executed successfully",
  "file": "/home/me/Downloads/Screenshots/Screenshot_2026-11-05-17:30.12.png",
  "state": {"recording": false, "paused": false}
}
```

On failure, `success` is `false` and `message` explains why. `file` is set only
by `screenshot`, and `state.recording_file` names the file of the current
recording.
