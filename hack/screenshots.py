#!/usr/bin/env python3
"""Render the README and social-preview images for record-gui.

Runs the `demo` build of sway-easyshot (staged monitors, windows and a
recording, so no real desktop content is captured), grabs each page on a
Hyprland session with grim and composes the results with Pillow.

Usage: python3 hack/screenshots.py   (or: make screenshots)
Needs: Hyprland, grim, ffmpeg, Pillow, Inter Display font, GTK4/libadwaita.
"""

import json
import os
import random
import subprocess
import sys
import tempfile
import time
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter, ImageFont

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / ".github" / "screenshots"
APP_CLASS = "com.chmouel.SwayEasyshot.Recorder"
CLIP = "recording-20261105-13h04.mp4"
WINDOW_RADIUS = 14


def font(size, weight="Bold"):
    """Inter Display at the given weight; Inter ships as a .ttc collection."""
    path = subprocess.run(["fc-match", "-f", "%{file}", f"Inter Display:style={weight}"],
                          capture_output=True, text=True).stdout
    for index in range(64):
        try:
            f = ImageFont.truetype(path, size, index=index)
        except OSError:
            break
        family, style = f.getname()
        if family == "Inter Display" and style == weight:
            return f
    return ImageFont.truetype(path, size)


def gradient(w, h, stops, angle_vertical=False):
    """Diagonal multi-stop gradient, drawn small and upscaled since it is smooth."""
    fw, fh = w, h
    w, h = max(2, fw // 8), max(2, fh // 8)
    base = Image.new("RGB", (w, h))
    px = base.load()
    for y in range(h):
        for x in range(w):
            t = (x / w * 0.65 + y / h * 0.35) if not angle_vertical else y / h
            for i in range(len(stops) - 1):
                (t0, c0), (t1, c1) = stops[i], stops[i + 1]
                if t0 <= t <= t1:
                    f = (t - t0) / (t1 - t0)
                    px[x, y] = tuple(int(c0[k] + (c1[k] - c0[k]) * f) for k in range(3))
                    break
    return base.resize((fw, fh), Image.BICUBIC)


def blobs(img, colours, seed):
    """Soft blurred light blobs, a nod to the GNOME/Adwaita wallpapers."""
    rnd = random.Random(seed)
    w, h = img.size
    layer = Image.new("RGBA", img.size, (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)
    for c in colours:
        r = rnd.randint(h // 4, h // 2)
        x, y = rnd.randint(0, w), rnd.randint(0, h)
        d.ellipse((x - r, y - r, x + r, y + r), fill=c + (90,))
    layer = layer.filter(ImageFilter.GaussianBlur(h // 8))
    return Image.alpha_composite(img.convert("RGBA"), layer).convert("RGB")


BACKDROP = [(0.0, (236, 72, 153)), (0.5, (124, 58, 237)), (1.0, (59, 130, 246))]


def mock_window(w, h, kind, rnd):
    """A plain dark Linux-style window: header bar, then content."""
    win = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    d = ImageDraw.Draw(win)
    d.rounded_rectangle((0, 0, w - 1, h - 1), radius=12, fill=(30, 30, 36))
    d.rounded_rectangle((0, 0, w - 1, 44), radius=12, fill=(44, 44, 52))
    d.rectangle((0, 30, w - 1, 44), fill=(44, 44, 52))
    for i in range(3):  # Adwaita-style minimise/maximise/close on the right
        cx = w - 26 - i * 30
        d.ellipse((cx - 9, 13, cx + 9, 31), fill=(64, 64, 74))
    palette = [(129, 140, 248), (52, 211, 153), (251, 191, 36), (244, 114, 182), (148, 163, 184)]
    y = 66
    if kind == "terminal":
        while y < h - 24:
            x = 22
            for _ in range(rnd.randint(1, 4)):
                seg = rnd.randint(30, 160)
                d.rounded_rectangle((x, y, min(x + seg, w - 22), y + 12), radius=6, fill=rnd.choice(palette))
                x += seg + 12
                if x > w - 60:
                    break
            y += 26
    else:  # document / browser
        d.rounded_rectangle((22, y, w - 22, y + h // 3), radius=10, fill=(56, 56, 70))
        y += h // 3 + 26
        while y < h - 24:
            d.rounded_rectangle((22, y, rnd.randint(w // 2, w - 22), y + 12), radius=6, fill=(88, 88, 102))
            y += 26
    return win


def mock_desktop(w, h, seed, wall):
    rnd = random.Random(seed)
    desk = blobs(gradient(w, h, wall), [(255, 255, 255), (255, 200, 240), (160, 200, 255)], seed).convert("RGBA")
    bar = ImageDraw.Draw(desk)
    bar.rectangle((0, 0, w, 34), fill=(18, 18, 22, 235))
    for i in range(5):
        bar.rounded_rectangle((14 + i * 34, 9, 38 + i * 34, 25), radius=6, fill=(124, 58, 237) if i == 1 else (70, 70, 80))
    for i, (kind, box) in enumerate([("terminal", (0.04, 0.10, 0.50, 0.92)), ("doc", (0.53, 0.10, 0.96, 0.92))]):
        x0, y0, x1, y1 = int(box[0] * w), int(box[1] * h), int(box[2] * w), int(box[3] * h)
        win = mock_window(x1 - x0, y1 - y0, kind, rnd)
        shadow = Image.new("RGBA", desk.size, (0, 0, 0, 0))
        ImageDraw.Draw(shadow).rounded_rectangle((x0 + 4, y0 + 10, x1 + 4, y1 + 10), radius=14, fill=(0, 0, 0, 110))
        desk = Image.alpha_composite(desk, shadow.filter(ImageFilter.GaussianBlur(14)))
        desk.alpha_composite(win, (x0, y0))
    return desk.convert("RGB")


def stage_assets(tmp):
    walls = {
        "DP-1": [(0.0, (49, 46, 129)), (0.6, (109, 40, 217)), (1.0, (219, 39, 119))],
        "HDMI-A-1": [(0.0, (12, 74, 110)), (0.6, (14, 116, 144)), (1.0, (16, 185, 129))],
    }
    for i, (name, wall) in enumerate(walls.items()):
        mock_desktop(960, 540, i + 3, wall).save(tmp / f"{name}.jpg", quality=92)
    frame = mock_desktop(1920, 1080, 3, walls["DP-1"])
    frame.save(tmp / "frame.png")
    subprocess.run(["ffmpeg", "-loglevel", "error", "-y", "-loop", "1", "-framerate", "2", "-i", str(tmp / "frame.png"),
                    "-t", "42", "-c:v", "libx264", "-pix_fmt", "yuv420p",
                    # Constant bitrate so the staged clip reports a realistic size.
                    "-b:v", "3500k", "-minrate", "3500k", "-maxrate", "3500k", "-bufsize", "1750k",
                    "-x264-params", "nal-hrd=cbr", str(tmp / CLIP)], check=True)


def hypr_window():
    for c in json.loads(subprocess.run(["hyprctl", "clients", "-j"], capture_output=True, text=True).stdout):
        if c["class"] == APP_CLASS and c.get("mapped", True) and not c.get("hidden", False):
            return c
    return None


def capture(binary, tmp, page):
    env = dict(os.environ, SWAY_EASYSHOT_DEMO=page, SWAY_EASYSHOT_DEMO_DIR=str(tmp))
    proc = subprocess.Popen([binary, "record-gui"], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        win = None
        for _ in range(50):
            time.sleep(0.2)
            win = hypr_window()
            if win:
                break
        if not win:
            sys.exit(f"record-gui window for {page!r} never appeared")
        time.sleep(2.5)  # thumbnails, staged selection and preview frame
        win = hypr_window()
        (x, y), (w, h) = win["at"], win["size"]
        shot = tmp / f"{page}.png"
        subprocess.run(["grim", "-g", f"{x},{y} {w}x{h}", str(shot)], check=True)
        return Image.open(shot).convert("RGBA")
    finally:
        proc.terminate()
        proc.wait(timeout=5)


def rounded(img, radius=WINDOW_RADIUS):
    mask = Image.new("L", img.size, 0)
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, img.width - 1, img.height - 1), radius=radius, fill=255)
    out = img.copy()
    out.putalpha(mask)
    return out


def paste_with_shadow(canvas, win, pos, blur=28, offset=(0, 18), alpha=120):
    shadow = Image.new("RGBA", canvas.size, (0, 0, 0, 0))
    x, y = pos
    ImageDraw.Draw(shadow).rounded_rectangle(
        (x + offset[0], y + offset[1], x + win.width + offset[0], y + win.height + offset[1]),
        radius=WINDOW_RADIUS, fill=(15, 8, 40, alpha))
    canvas.alpha_composite(shadow.filter(ImageFilter.GaussianBlur(blur)))
    canvas.alpha_composite(win, pos)


def backdrop(w, h, seed):
    return blobs(gradient(w, h, BACKDROP), [(255, 255, 255), (255, 170, 220), (150, 190, 255)], seed).convert("RGBA")


def hero(shots):
    order = ["monitor", "window", "result"]
    wins = [rounded(shots[p]) for p in order]
    gap, margin = 56, 72
    w = sum(i.width for i in wins) + gap * (len(wins) - 1) + margin * 2
    h = max(i.height for i in wins) + margin * 2 + 20
    canvas = backdrop(w, h, 11)
    x, tallest = margin, max(i.height for i in wins)
    for win in wins:
        paste_with_shadow(canvas, win, (x, margin + (tallest - win.height) // 2))
        x += win.width + gap
    return canvas.convert("RGB")


def logo(size):
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    d.rounded_rectangle((0, 0, size - 1, size - 1), radius=size // 4.5, fill=(30, 27, 46))
    m = size // 5
    d.rounded_rectangle((m, m + size // 10, size - m, size - m - size // 10), radius=size // 12, outline=(196, 181, 253), width=max(3, size // 28))
    r = size // 9
    c = size // 2
    d.ellipse((c - r, c - r, c + r, c + r), fill=(244, 63, 94))
    return img


def social(shots):
    w, h = 1280, 640
    canvas = backdrop(w, h, 5)
    canvas.alpha_composite(logo(96), (72, 96))
    d = ImageDraw.Draw(canvas)
    d.text((72, 222), "sway-easyshot", font=font(76, "Bold"), fill="white")
    tagline = "Screenshots and GPU-accelerated screen\nrecordings for sway and Hyprland"
    d.multiline_text((74, 324), tagline, font=font(30, "Medium"), fill=(255, 255, 255, 235), spacing=10)
    x, y = 74, 438
    chip_font = font(22, "SemiBold")
    for chip in ("Hyprland", "sway", "GTK4", "gpu-screen-recorder"):
        tw = d.textlength(chip, font=chip_font)
        d.rounded_rectangle((x, y, x + tw + 32, y + 44), radius=22, fill=(20, 12, 46, 150))
        d.text((x + 16, y + 9), chip, font=chip_font, fill="white")
        x += tw + 32 + 12
    win = rounded(shots["monitor"])
    scale = (h - 70) / win.height
    win = win.resize((int(win.width * scale), int(win.height * scale)), Image.LANCZOS)
    paste_with_shadow(canvas, win, (w - win.width - 64, (h - win.height) // 2))
    return canvas.convert("RGB")


def main():
    if not os.environ.get("HYPRLAND_INSTANCE_SIGNATURE"):
        sys.exit("hack/screenshots.py needs a Hyprland session")
    OUT.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory() as d:
        tmp = Path(d)
        binary = str(tmp / "sway-easyshot-demo")
        subprocess.run(["go", "build", "-tags", "demo", "-o", binary, "./cmd/sway-easyshot"], cwd=ROOT, check=True)
        stage_assets(tmp)
        shots = {p: capture(binary, tmp, p) for p in ("monitor", "window", "result")}
        for page, img in shots.items():
            rounded(img).save(OUT / f"record-gui-{page}.png")
        hero(shots).save(OUT / "record-gui.png", optimize=True)
        social(shots).save(OUT / "social-preview.png", optimize=True)
    print(f"wrote images to {OUT.relative_to(ROOT)}")


if __name__ == "__main__":
    main()
