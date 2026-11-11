//go:build !nogui

// Command mattebench runs the matting models over saved pictures and reports
// latency and throughput. It writes each alpha matte and a composite over a
// checkerboard so the quality can be compared by eye. It never captures a
// camera or reads the desktop.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // registers the JPEG decoder for the input pictures
	"image/png"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chmouel/tequilashot/internal/segment"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	input := flag.String("input", "", "picture file, or directory of PNG/JPEG pictures")
	out := flag.String("out", "tmp/mattebench", "directory for the mattes and composites")
	models := flag.String("models", "u2net,modnet", "comma-separated models to run")
	runs := flag.Int("runs", 10, "timed runs per picture and model")
	warmup := flag.Int("warmup", 1, "untimed runs before measuring")
	flag.Parse()

	pictures, err := collect(*input)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}

	var results []result
	for _, name := range strings.Split(*models, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		got, err := benchmark(name, pictures, *out, *runs, *warmup)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		results = append(results, got...)
	}

	for _, r := range results {
		report(r)
	}
	summarise(results)
	compare(results)
	return nil
}

// collect returns the pictures named by path, in a stable order.
func collect(path string) ([]string, error) {
	if path == "" {
		return nil, fmt.Errorf("pass -input with a picture or a directory of pictures")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".png", ".jpg", ".jpeg":
			paths = append(paths, filepath.Join(path, e.Name()))
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no PNG or JPEG pictures in %s", path)
	}
	sort.Strings(paths)
	return paths, nil
}

type result struct {
	model    string
	file     string
	size     string
	times    []time.Duration
	coverage float64
	matte    *image.Gray
}

func benchmark(name string, pictures []string, out string, runs, warmup int) ([]result, error) {
	if runs < 1 {
		return nil, fmt.Errorf("-runs must be at least 1")
	}
	fmt.Printf("model %s: %s\n", name, modelPath(name))
	matter, err := segment.NewWithModel(name)
	if err != nil {
		return nil, err
	}
	defer matter.Close()

	var results []result
	for _, path := range pictures {
		picture, err := load(path)
		if err != nil {
			return nil, err
		}
		matte, err := matter.Matte(picture)
		if err != nil {
			return nil, err
		}
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if err := save(filepath.Join(out, base+"-"+name+"-matte.png"), matte); err != nil {
			return nil, err
		}

		matter.SetBackground(checkerboard(picture.Bounds().Dx(), picture.Bounds().Dy()), false)
		composite, err := matter.CutImage(fresh(picture))
		matter.SetBackground(nil, false)
		if err != nil {
			return nil, err
		}
		if err := save(filepath.Join(out, base+"-"+name+"-composite.png"), composite); err != nil {
			return nil, err
		}

		for range warmup {
			if _, err := matter.CutImage(fresh(picture)); err != nil {
				return nil, err
			}
		}
		times := make([]time.Duration, 0, runs)
		for range runs {
			frame := fresh(picture)
			start := time.Now()
			_, err := matter.CutImage(frame)
			times = append(times, time.Since(start))
			if err != nil {
				return nil, err
			}
		}
		b := picture.Bounds()
		results = append(results, result{
			model: name, file: path, size: fmt.Sprintf("%dx%d", b.Dx(), b.Dy()),
			times: times, coverage: foreground(matte), matte: matte,
		})
	}
	return results, nil
}

func modelPath(name string) string {
	if name == "modnet" {
		return segment.ModnetModelPath()
	}
	return segment.ModelPath()
}

// fresh returns a private copy, so a timed run never sees pixels a previous
// run has already premultiplied.
func fresh(src image.Image) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Rect, src, b.Min, draw.Src)
	return out
}

func load(path string) (image.Image, error) {
	// #nosec G304 -- the picture paths come from the command line.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	return img, nil
}

func save(path string, img image.Image) error {
	// #nosec G304 -- the output paths come from -out and the picture names.
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func foreground(matte *image.Gray) float64 {
	if len(matte.Pix) == 0 {
		return 0
	}
	on := 0
	for _, v := range matte.Pix {
		if v > 127 {
			on++
		}
	}
	return float64(on) / float64(len(matte.Pix))
}

func report(r result) {
	p50, p95 := percentiles(r.times)
	mean := average(r.times)
	fmt.Printf("%s\t%s\t%s\tmin=%s\tp50=%s\tp95=%s\tmean=%s\t%.1f frames/s\tforeground=%.3f\n",
		r.model, filepath.Base(r.file), r.size,
		round(minimum(r.times)), round(p50), round(p95), round(mean), framesPerSecond(mean), r.coverage)
}

func summarise(results []result) {
	sums := map[string][]time.Duration{}
	for _, r := range results {
		sums[r.model] = append(sums[r.model], r.times...)
	}
	names := make([]string, 0, len(sums))
	for name := range sums {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Println("summary")
	for _, name := range names {
		mean := average(sums[name])
		fmt.Printf("%s\tmean=%s\t%.1f frames/s\n", name, round(mean), framesPerSecond(mean))
	}
}

// compare prints the mean absolute alpha difference between every pair of
// models for each picture. It measures how much the models disagree, not which
// is better.
func compare(results []result) {
	byFile := map[string]map[string]*image.Gray{}
	for _, r := range results {
		if byFile[r.file] == nil {
			byFile[r.file] = map[string]*image.Gray{}
		}
		byFile[r.file][r.model] = r.matte
	}
	files := make([]string, 0, len(byFile))
	for file := range byFile {
		files = append(files, file)
	}
	sort.Strings(files)
	fmt.Println("disagreement")
	for _, file := range files {
		matteByModel := byFile[file]
		names := make([]string, 0, len(matteByModel))
		for name := range matteByModel {
			names = append(names, name)
		}
		sort.Strings(names)
		for i := range names {
			for j := i + 1; j < len(names); j++ {
				a, b := matteByModel[names[i]], matteByModel[names[j]]
				diff, ok := meanDifference(a, b)
				if !ok {
					continue
				}
				fmt.Printf("%s\t%s vs %s\tmean alpha difference=%.4f\n",
					filepath.Base(file), names[i], names[j], diff)
			}
		}
	}
}

func meanDifference(a, b *image.Gray) (float64, bool) {
	if a.Rect != b.Rect {
		return 0, false
	}
	if len(a.Pix) == 0 {
		return 0, false
	}
	var sum float64
	for i := range a.Pix {
		d := float64(a.Pix[i]) - float64(b.Pix[i])
		sum += max(d, -d)
	}
	return sum / float64(len(a.Pix)) / 255, true
}

func average(times []time.Duration) time.Duration {
	if len(times) == 0 {
		return 0
	}
	var sum time.Duration
	for _, t := range times {
		sum += t
	}
	return sum / time.Duration(len(times))
}

func minimum(times []time.Duration) time.Duration {
	if len(times) == 0 {
		return 0
	}
	smallest := times[0]
	for _, t := range times[1:] {
		smallest = min(smallest, t)
	}
	return smallest
}

func framesPerSecond(mean time.Duration) float64 {
	if mean <= 0 {
		return 0
	}
	return 1 / mean.Seconds()
}

func percentiles(times []time.Duration) (time.Duration, time.Duration) {
	if len(times) == 0 {
		return 0, 0
	}
	sorted := append([]time.Duration(nil), times...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	at := func(p float64) time.Duration {
		i := int(p * float64(len(sorted)-1))
		return sorted[i]
	}
	return at(0.5), at(0.95)
}

func round(d time.Duration) string {
	return d.Round(100 * time.Microsecond).String()
}

func checkerboard(w, h int) *image.RGBA {
	const square = 32
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := byte(0x50)
			if (x/square+y/square)%2 == 1 {
				v = 0xc0
			}
			o := out.PixOffset(x, y)
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = v, v, v, 255
		}
	}
	return out
}
