package denoise

import (
	"math"
	"math/cmplx"
	"slices"
	"time"

	"gonum.org/v1/gonum/dsp/fourier"
)

// Keyboard clicks are found from the key times noted during the recording.
// The times are on the recording's clock, which starts a little before the
// recorder captures any sound, and each keyboard adds its own latency, so the
// times are first lined up with the sound as a whole and then each one is
// moved to the click nearest to it.

const onsetHop = 240 // 5 ms at 48 kHz

// Lags searched when lining the key times up with the sound.
const (
	minLag = -100 // -0.5 s
	maxLag = 400  // +2 s
)

// onsets turns sound into a per-onsetHop measure of sudden rises in high
// frequency energy, where key clicks stand out from speech.
type onsets struct {
	prev1, prev2 float32
	acc          float64
	n            int
	levels       []float64
	strength     []float32
}

func (o *onsets) add(x []float32) {
	for _, v := range x {
		// A second difference rises 12 dB per octave, leaving mostly the
		// highs.
		d := v - 2*o.prev1 + o.prev2
		o.prev2, o.prev1 = o.prev1, v
		o.acc += float64(d) * float64(d)
		o.n++
		if o.n == onsetHop {
			level := math.Log10(o.acc/onsetHop + 1e-12)
			rise := 0.0
			if k := len(o.levels); k >= 2 {
				rise = max(0, level-min(o.levels[k-1], o.levels[k-2]))
			}
			o.levels = append(o.levels, level)
			o.strength = append(o.strength, float32(rise))
			o.acc, o.n = 0, 0
		}
	}
}

// align returns how far the sound lags behind the key times, or false when
// the key times do not line up with the sound convincingly.
func align(times []time.Duration, strength []float32) (time.Duration, bool) {
	if len(times) < 4 || len(strength) == 0 {
		return 0, false
	}
	at := func(i int) float32 {
		if i < 0 || i >= len(strength) {
			return 0
		}
		return strength[i]
	}
	scores := make([]float64, 0, maxLag-minLag+1)
	best, bestLag := -1.0, 0
	for lag := minLag; lag <= maxLag; lag++ {
		var s float64
		for _, t := range times {
			k := int(t/(onsetHop*time.Second/sampleRate)) + lag
			s += float64(max(at(k-1), at(k), at(k+1)))
		}
		scores = append(scores, s)
		if s > best {
			best, bestLag = s, lag
		}
	}
	var mean, sq float64
	for _, s := range scores {
		mean += s
	}
	mean /= float64(len(scores))
	for _, s := range scores {
		sq += (s - mean) * (s - mean)
	}
	std := math.Sqrt(sq / float64(len(scores)))
	if std == 0 || (best-mean)/std < 4 {
		return 0, false
	}
	return time.Duration(bestLag) * onsetHop * time.Second / sampleRate, true
}

// Spans around a click, in samples, measured from its start.
const (
	searchBefore = 20 * sampleRate / 1000
	searchAfter  = 30 * sampleRate / 1000
	clickBefore  = 3 * sampleRate / 1000
	clickAfter   = 40 * sampleRate / 1000
	refBefore    = 48 * sampleRate / 1000
	refAfter     = 96 * sampleRate / 1000
	fadeLen      = 2 * sampleRate / 1000
	// repairReach bounds the samples a repair reads or changes either side of
	// the key time.
	repairReach = searchAfter + refAfter + repairN
	repairN     = 256
	repairHop   = 64
	// A click must stand this far above the sound around it, in power.
	minProminence = 4
	// Bins louder than the sound either side of the click by more than this,
	// in magnitude, are brought down to it. Speech swells by less in the
	// span of a click; clicks rise far more.
	capMargin = 3
)

// highEnergy returns the energy of the second difference of x over [lo, hi).
func highEnergy(x []float32, lo, hi int) []float64 {
	e := make([]float64, 0, hi-lo)
	for i := lo; i < hi; i++ {
		var d float64
		if i >= 2 && i < len(x) {
			d = float64(x[i] - 2*x[i-1] + x[i-2])
		}
		e = append(e, d*d)
	}
	return e
}

// findClick looks for the click near sample p of mix, the channels mixed
// down, and returns where it starts, or false when nothing there stands out.
func findClick(mix []float32, p int) (int, bool) {
	lo, hi := p-searchBefore, p+searchAfter
	if lo-refBefore < 0 || hi+refAfter > len(mix) {
		return 0, false
	}
	e := highEnergy(mix, lo, hi)
	// Smooth over 1 ms and find the loudest moment.
	const w = sampleRate / 1000
	peak, at := 0.0, 0
	var run float64
	for i, v := range e {
		run += v
		if i >= w {
			run -= e[i-w]
		}
		if run > peak {
			peak, at = run, i
		}
	}
	start := lo + max(0, at-w)
	around := slices.Concat(
		highEnergy(mix, start-refBefore, start-clickBefore-w),
		highEnergy(mix, start+clickAfter, min(len(mix), start+clickAfter+refBefore)),
	)
	if len(around) == 0 {
		return 0, false
	}
	slices.Sort(around)
	floor := around[len(around)/2] * w
	if peak < minProminence*floor || peak == 0 {
		return 0, false
	}
	return start, true
}

var (
	repairFFT    = fourier.NewFFT(repairN)
	repairWindow = func() []float64 {
		w := make([]float64, repairN)
		for i := range w {
			w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*(float64(i)+0.5)/repairN)
		}
		return w
	}()
)

// repairClick tones down the click starting at sample q of x: in each short
// frame across the click, every frequency much louder than the sound just
// before and after it is brought down, keeping its phase. Only
// samples within the click are changed, faded in and out at the edges.
func repairClick(x []float32, q int) {
	lo, hi := q-refBefore, q+refAfter
	if lo < 0 || hi > len(x) {
		return
	}
	n := (hi - lo - repairN) / repairHop
	spec := make([][]complex128, n+1)
	frame := make([]float64, repairN)
	for k := range spec {
		for i := range frame {
			frame[i] = float64(x[lo+k*repairHop+i]) * repairWindow[i]
		}
		spec[k] = repairFFT.Coefficients(nil, frame)
	}
	kind := func(k int) int {
		s := lo + k*repairHop
		switch {
		case s+repairN <= q-clickBefore:
			return -1
		case s >= q+clickAfter:
			return 1
		}
		return 0
	}
	bins := repairN/2 + 1
	before, after := make([]float64, bins), make([]float64, bins)
	for f := range bins {
		var b, a []float64
		for k, c := range spec {
			switch kind(k) {
			case -1:
				b = append(b, cmplx.Abs(c[f]))
			case 1:
				a = append(a, cmplx.Abs(c[f]))
			}
		}
		before[f], after[f] = median(b), median(a)
	}
	first, last := -1, -1
	for k := range spec {
		if kind(k) == 0 {
			if first < 0 {
				first = k
			}
			last = k
		}
	}
	if first < 0 {
		return
	}
	for k := first; k <= last; k++ {
		for f := range bins {
			ref := max(before[f], after[f]) * capMargin
			if m := cmplx.Abs(spec[k][f]); m > ref {
				spec[k][f] *= complex(ref/m, 0)
			}
		}
	}
	out := make([]float64, hi-lo)
	norm := make([]float64, hi-lo)
	for k, c := range spec {
		repairFFT.Sequence(frame, c)
		for i, v := range frame {
			out[k*repairHop+i] += v / repairN * repairWindow[i]
			norm[k*repairHop+i] += repairWindow[i] * repairWindow[i]
		}
	}
	from, to := q-clickBefore-fadeLen, q+clickAfter+fadeLen
	for j := from; j < to; j++ {
		i := j - lo
		if norm[i] < 1e-3 {
			continue
		}
		g := 1.0
		if d := j - from; d < fadeLen {
			g = 0.5 - 0.5*math.Cos(math.Pi*float64(d)/fadeLen)
		} else if d := to - 1 - j; d < fadeLen {
			g = 0.5 - 0.5*math.Cos(math.Pi*float64(d)/fadeLen)
		}
		x[j] = float32((1-g)*float64(x[j]) + g*out[i]/norm[i])
	}
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	slices.Sort(v)
	return v[len(v)/2]
}
