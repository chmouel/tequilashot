# Developing Tequilashot

## Building and checking

```sh
make build    # bin/tequilashot, including the capture dialogue
make test     # go test ./...
make lint     # golangci-lint run --fix ./...
make format   # gofumpt -w .
make sanity   # lint, format and test
```

The trimmer's playback check needs FFmpeg, a GTK media backend and a display.
Run it on an isolated display with:

```sh
dbus-run-session -- xvfb-run -a env GDK_BACKEND=x11 GSK_RENDERER=cairo GTK_A11Y=none \
  TEQUILASHOT_TEST_GUI=1 go test ./internal/gui -run '^TestTrim(Playback|Import|HistoryInteraction|FrameInteraction|SparseAndPlayingFrames|ShortPreview)$' -count=1
```

Frame interaction checks read binary frame markers from the displayed texture
and compare exported boundary frames. Paused stepping uses FFmpeg stills because
GTK's GStreamer backend can skip overlapping B-frames in variable-rate videos.
Frame timestamps are loaded in bounded windows; ordinary playback remains with GTK.

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

`internal/prefs` holds the preferences edited in the Preferences dialogue. It
is pure Go so the `nogui` build keeps it. The dialogue saves
`preferences.json` and the daemon reloads it before every command and
clean-up pass, save for `waybar-status`, which answers from the settings
already in force so the bar need not wait on the file. Argument builders
should read `prefs.Current()` once per operation and keep that snapshot for
its lifetime. Run helper programs through `prefs.Bin(name)` so a configured
location is honoured.

`internal/onnx` loads ONNX Runtime once for both `internal/segment` and
`internal/denoise`; anything importing it needs cgo and so stays out of the
`nogui` build, where noise removal reports that it is unavailable.
`internal/denoise` decodes the sound with FFmpeg, turns down the keyboard
clicks, runs DeepFilterNet3 a segment at a time and pipes the result back into
FFmpeg. The click times come from `internal/keylog`, which the daemon starts
whilst the microphone is recorded and which keeps only key times, in a small
file per recording under `~/.local/state/tequilashot/keytimes`.

`internal/segment` runs MODNet by default. `internal/segment/modnet.go`
implements the same `matting` interface as the older U²-Net path, so the
camera, recording and virtual-camera paths are shared; see
[MODNet matting](#modnet-matting).

## MODNet matting

The camera matting used by Remove background and `--camera-background` runs
MODNet. U²-Net is kept as an alternative and picks the same `matting` interface.

The model is chosen by, in order: `--camera-matting-model`
(`modnet` or `u2net`, on the recording and virtual-camera commands),
`TEQUILASHOT_MATTING_MODEL`, then MODNet. The flag travels to the daemon as the
`camera_matting_model` request option and on to the camera bubble, so it
overrides the environment of the daemon that runs the bubble. An unknown value
is refused rather than falling back, and naming a model without background
removal or replacement is refused like `--camera-no-ring` is. For live use, set
the environment of the daemon that launches the bubble, then restart that
daemon when no capture is running.

An installation upgraded from an earlier release may have only the U²-Net file.
When MODNet is the default rather than explicitly requested, its file is
missing and the U²-Net file exists, matting uses U²-Net and logs a warning
naming both files and how to download MODNet. Only a missing file falls back:
any other stat error is reported, so a permissions or symlink problem cannot
change the model quietly. Once MODNet is requested by the flag or the
environment, a missing file is an error, as is a missing U²-Net file when that
model is requested.

`TEQUILASHOT_MODNET_MODEL` names the model file, then the `modnet_model`
preference, then `~/.local/share/tequilashot/modnet.onnx`. Nothing is
downloaded automatically, but the Download button on the Background Removal page
of Preferences and the camera dialogue fetch the model through
`internal/segment/download.go`, which verifies the SHA-256 and the size before
the file replaces an existing one. The camera dialogue offers that download
only when MODNet is the model in use; when a model chosen by the flag or the
environment is missing or invalid it names the problem and the file instead,
since fetching MODNet would not help. The U²-Net path still reads the older
`segmentation_model` preference and `TEQUILASHOT_SEGMENTATION_MODEL`. Model
files stay out of git.

The model is an ONNX conversion of MODNet published by Hugging Face's Xenova
project, under Apache-2.0 like the upstream
[ZHKKKe/MODNet](https://github.com/ZHKKKe/MODNet). The download is pinned by
commit rather than a moving branch:
`https://huggingface.co/Xenova/modnet/resolve/fa2fa546052fba4c08921230a26cc69a333fca12/onnx/model.onnx`,
SHA-256 `07c308cf0fc7e6e8b2065a12ed7fc07e1de8febb7dc7839d7b7f15dd66584df9`,
25,888,640 bytes. Installing it by hand is also possible:

```sh
mkdir -p ~/.local/share/tequilashot
curl -fL --proto '=https' -o ~/.local/share/tequilashot/modnet.onnx \
  https://huggingface.co/Xenova/modnet/resolve/fa2fa546052fba4c08921230a26cc69a333fca12/onnx/model.onnx
sha256sum ~/.local/share/tequilashot/modnet.onnx
# expect 07c308cf0fc7e6e8b2065a12ed7fc07e1de8febb7dc7839d7b7f15dd66584df9
```

The graph takes `input`, a float32 NCHW tensor of
shape `[1, 3, H, W]` with RGB scaled to -1..1, and returns `output`, a float32
tensor of shape `[1, 1, H, W]` whose last node is a sigmoid: an alpha matte
already in 0..1. The implementation therefore uses the output directly,
without the min-max stretch the U²-Net path needs.

Geometry follows the model's own `onnx/inference_onnx.py`: the short edge
becomes 512 unless the picture straddles 512 (one edge below, one above), then
both edges are rounded down to a multiple of 32 and kept at 32 or more. Input
is resampled bilinearly; the output alpha is resampled to the frame with the
same helper the U²-Net path uses. The official script uses OpenCV
`INTER_AREA`, so edge pixels may differ slightly.

### Comparing the models

`hack/mattebench` runs either model or both over saved pictures or a directory
of extracted camera frames. It writes an alpha matte and a composite over a
checkerboard for each picture and model, and reports latency, throughput,
foreground coverage and the mean alpha difference between models.

```sh
go run ./hack/mattebench -input ~/Pictures/frames -out tmp/mattebench -runs 20
```

`-models modnet` checks the default alone. Timings cover preprocessing,
inference and blending on an already decoded picture; decoding and PNG output
are outside them. It needs both model files and downloads neither, and it
never opens the camera. On one desktop CPU, on a 460 by 460 portrait with
three timed runs each, U²-Net averaged 244 ms (4.1 frames a second) and MODNet
256 ms (3.9 frames a second), with a mean alpha difference of 0.0076. These
figures say how fast the models ran on that machine, not which matte is
better, and the order between them has changed between machines and runs:
judge quality from the written mattes and composites, and measure on your own
frames before relying on it.

Limitations: the conversion is third-party rather than the official PyTorch
checkpoint, accuracy has not been measured against a labelled matting dataset,
and MODNet is tuned for one person.

## Screenshots

`make screenshots` rebuilds the dialogue and social-preview images in
`.github/screenshots`. It needs a Hyprland session, `grim`, FFmpeg,
`rsvg-convert`, Pillow, the Inter Display font and the GTK development files.
The script builds the `demo` binary, opens each page in turn and captures it.
The demo stages sample windows, a webcam silhouette and media files, so no real
desktop content is recorded.

Leave the desktop alone while it runs. If another floating window overlaps the
dialogue, or its workspace is not visible, the script stops without writing any
image. Run `make optimize` afterwards to compress the PNG files with
`pngquant`.

To look at one staged page by hand, build the demo binary and set
`TEQUILASHOT_DEMO` to a page name (`monitor`, `camera-circle`, `window`,
`result`, `screenshot`, `screenshot-result`, `replay`, `replay-active`,
`replay-result`, `trim`, `about`, `preferences` or `preferences-<page>`,
where the page is `recording`, `screenshots`, `advanced`, `encoding`,
`programs` or `background`) and
`TEQUILASHOT_DEMO_DIR` to a directory holding the staged assets that
`hack/screenshots.py` writes.

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
| `replay-start` | Buffers `output`, or a monitor picked interactively; `use_current_screen` selects the focused monitor. |
| `replay-save` | Saves the available replay buffer, returns the completed clip in `file`, and continues buffering. |
| `replay-stop` | Stops buffering and discards the unsaved buffer. |
| `waybar-status` | Returns the Waybar status as a JSON string in `message`. |
| `preferences-status` | Returns, as a JSON string in `message`, the daemon's save folder, whether clean-up is active, and which `TEQUILASHOT_*` environment overrides it sees. |

Recording actions reply once the recorder is running, not when the recording
ends. Watch `state.recording` with `waybar-status` to learn when it stops.

Replay starts reply once the recorder's IPC socket is available and the
recorder has passed its startup check. Replay and
normal recording share one session slot. Replay saves and stops are serialised,
with a 30-second deadline. Clients should allow five additional seconds for
the daemon to report a timeout. A lost save acknowledgement blocks further
saves until replay is stopped and restarted, preventing a late export from
being overwritten. Stopping recovers completed exports left in staging.

### Options

| Option | Type | Used by |
| --- | --- | --- |
| `delay` | number (seconds) | Captures and recordings |
| `output` | string, an output name | `screenshot`, `movie-screen`, `replay-start` |
| `geometry` | string, `"X,Y WxH"` | `screenshot`, `movie-geometry` |
| `use_current_screen` | boolean | `current-screen-clipboard`, `movie-screen`, `toggle-record`, `replay-start` |
| `crop_top` | number (pixels) | `movie-screen`, `toggle-record` |
| `seconds` | number; 30, 60 or 120, default 60 | `replay-start` |
| `audio`, `mic` | boolean | Recordings and replay |
| `mic_device` | string, a gpu-screen-recorder audio device id | Recordings and replay |
| `camera` | boolean, enables the `camera_*` options | Recordings |
| `camera_device` | string, `/dev/videoN` | Recordings |
| `camera_position` | `top-left`, `top-right`, `bottom-left`, `bottom-right` | Recordings |
| `camera_size` | `small`, `medium`, `large` | Recordings |
| `camera_shape` | `square`, `circle` | Recordings |
| `camera_mirror` | boolean | Recordings |
| `camera_background` | string, absolute path of a PNG or JPEG shown behind the person; needs the circle shape, excludes `camera_remove_background` | Recordings and virtual camera |
| `camera_no_ring` | boolean; hides the ring, needs `camera_remove_background` or `camera_background` | Recordings and virtual camera |
| `camera_matting_model` | `modnet` or `u2net`; needs `camera_remove_background` or `camera_background`, overrides `TEQUILASHOT_MATTING_MODEL` | Recordings and virtual camera |
| `camera_remove_background` | boolean; needs the circle shape (always set for the virtual camera) | Recordings and virtual camera |
| `quiet_finish` | boolean; the finish notification omits its file actions because the client shows the result | Recordings |
| `start_action` | string, a recording action | `toggle-record` |
| `icons` | object with `Idle`, `Recording`, `Paused`, `Countdown`, `Live` and `Replay` strings | `waybar-status` |

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
by `screenshot` and `replay-save`, and `state.recording_file` names the file of
the current recording. Active replay adds `state.replay: true`,
`state.replay_output` and `state.replay_seconds`; it does not set
`state.recording` or `state.recording_file`. Replay fields are omitted when
inactive.
