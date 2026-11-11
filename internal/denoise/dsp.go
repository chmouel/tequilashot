package denoise

import (
	"errors"
	"math"
	"math/cmplx"

	"gonum.org/v1/gonum/dsp/fourier"
)

// Signal processing around the DeepFilterNet3 network, following the
// reference implementation (libDF): a 960-point STFT with a 480-sample hop
// and a Vorbis window, 32 ERB bands, and a fifth-order deep filter over the
// lowest 96 bins with a lookahead of two frames.
const (
	sampleRate  = 48000
	fftSize     = 960
	hopSize     = 480
	freqBins    = fftSize/2 + 1
	erbBands    = 32
	minErbBins  = 2
	dfBins      = 96
	dfOrder     = 5
	dfLookahead = 2
	dfLookback  = dfOrder - 1 - dfLookahead
	normAlpha   = 0.99
)

// model runs the network over t frames of features and returns the ERB mask
// (t×32) and the deep-filter coefficients (order×t×96×2).
type model func(featErb, featSpec []float32, t int) (mask, coefs []float32, err error)

var (
	vorbisWindow = makeVorbisWindow()
	erbWidths    = makeErbWidths()
)

func makeVorbisWindow() []float64 {
	w := make([]float64, fftSize)
	for i := range w {
		s := math.Sin(math.Pi * (float64(i) + 0.5) / fftSize)
		w[i] = math.Sin(math.Pi / 2 * s * s)
	}
	return w
}

func freqToErb(f float64) float64 { return 9.265 * math.Log1p(f/(24.7*9.265)) }
func erbToFreq(e float64) float64 { return 24.7 * 9.265 * (math.Exp(e/9.265) - 1) }

// makeErbWidths splits the spectrum into bands equally spaced on the ERB
// scale, each at least minErbBins wide.
func makeErbWidths() []int {
	low, high := freqToErb(0), freqToErb(sampleRate/2)
	step := (high - low) / erbBands
	widths := make([]int, erbBands)
	prev, over := 0, 0
	for i := 1; i <= erbBands; i++ {
		fb := int(math.Round(erbToFreq(low+float64(i)*step) / (float64(sampleRate) / fftSize)))
		n := fb - prev - over
		if n < minErbBins {
			over = minErbBins - n
			n = minErbBins
		} else {
			over = 0
		}
		widths[i-1] = n
		prev = fb
	}
	widths[erbBands-1]++
	sum := 0
	for _, w := range widths {
		sum += w
	}
	widths[erbBands-1] -= sum - freqBins
	return widths
}

// analyse returns the STFT of x, padded with one window of silence so that
// the last samples are fully covered, with frame t holding samples up to
// (t+1)·hop.
func analyse(x []float32) [][]complex128 {
	fft := fourier.NewFFT(fftSize)
	frames := (len(x)+fftSize)/hopSize + 1
	buf := make([]float64, fftSize)
	spec := make([][]complex128, frames)
	const norm = 2.0 * hopSize / (fftSize * fftSize)
	for t := range spec {
		start := (t+1)*hopSize - fftSize
		for i := range buf {
			buf[i] = 0
			if j := start + i; j >= 0 && j < len(x) {
				buf[i] = float64(x[j]) * vorbisWindow[i]
			}
		}
		c := fft.Coefficients(nil, buf)
		for i := range c {
			c[i] *= norm
		}
		spec[t] = c
	}
	return spec
}

// synthesise overlaps and adds the inverse of spec at the positions analyse
// took each frame from, and returns the first n samples.
func synthesise(spec [][]complex128, n int) []float32 {
	fft := fourier.NewFFT(fftSize)
	out := make([]float64, (len(spec)+1)*hopSize)
	buf := make([]float64, fftSize)
	for t, c := range spec {
		fft.Sequence(buf, c)
		start := (t+1)*hopSize - fftSize
		for i, v := range buf {
			if j := start + i; j >= 0 && j < len(out) {
				out[j] += v * vorbisWindow[i]
			}
		}
	}
	res := make([]float32, n)
	for i := range res {
		res[i] = float32(out[i])
	}
	return res
}

// features computes the normalised inputs of the network: ERB band energies
// in dB less their running mean, and the lowest bins of the spectrum divided
// by their running magnitude.
func features(spec [][]complex128) (featErb, featSpec []float32) {
	t := len(spec)
	featErb = make([]float32, t*erbBands)
	featSpec = make([]float32, 2*t*dfBins)
	meanState := make([]float64, erbBands)
	for b := range meanState {
		meanState[b] = -60 - 30*float64(b)/(erbBands-1)
	}
	unitState := make([]float64, dfBins)
	for f := range unitState {
		unitState[f] = 0.001 - 0.0009*float64(f)/(dfBins-1)
	}
	for i, frame := range spec {
		bin := 0
		for b, width := range erbWidths {
			var e float64
			for range width {
				v := frame[bin]
				e += real(v)*real(v) + imag(v)*imag(v)
				bin++
			}
			db := 10 * math.Log10(e/float64(width)+1e-10)
			meanState[b] = db*(1-normAlpha) + meanState[b]*normAlpha
			featErb[i*erbBands+b] = float32((db - meanState[b]) / 40)
		}
		for f := range dfBins {
			unitState[f] = cmplx.Abs(frame[f])*(1-normAlpha) + unitState[f]*normAlpha
			v := frame[f] / complex(math.Sqrt(unitState[f]), 0)
			featSpec[i*dfBins+f] = float32(real(v))
			featSpec[(t+i)*dfBins+f] = float32(imag(v))
		}
	}
	return featErb, featSpec
}

// enhance cleans one channel of 48 kHz samples with the network m.
func enhance(x []float32, m model) ([]float32, error) {
	spec := analyse(x)
	t := len(spec)
	featErb, featSpec := features(spec)
	mask, coefs, err := m(featErb, featSpec, t)
	if err != nil {
		return nil, err
	}
	if len(mask) != t*erbBands || len(coefs) != dfOrder*t*dfBins*2 {
		return nil, errors.New("the noise-removal model returned an unexpected shape")
	}
	out := make([][]complex128, t)
	for i := range out {
		frame := make([]complex128, freqBins)
		bin := 0
		for b, width := range erbWidths {
			g := complex(float64(mask[i*erbBands+b]), 0)
			for range width {
				frame[bin] = spec[i][bin] * g
				bin++
			}
		}
		for f := range dfBins {
			var y complex128
			for o := range dfOrder {
				src := i - dfLookback + o
				if src < 0 || src >= t {
					continue
				}
				k := ((o*t+i)*dfBins + f) * 2
				y += spec[src][f] * complex(float64(coefs[k]), float64(coefs[k+1]))
			}
			frame[f] = y
		}
		out[i] = frame
	}
	return synthesise(out, len(x)), nil
}
