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

# Waybar integration
sway-easyshot waybar-status
sway-easyshot waybar-status --follow

# OBS integration
sway-easyshot obs-toggle-recording
sway-easyshot obs-toggle-pause
```

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
