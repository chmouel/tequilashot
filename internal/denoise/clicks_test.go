package denoise

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

// identity is a model that leaves the sound as it is.
func identity(_, _ []float32, n int) ([]float32, []float32, error) {
	mask := make([]float32, n*erbBands)
	for i := range mask {
		mask[i] = 1
	}
	coefs := make([]float32, dfOrder*n*dfBins*2)
	for t := range n {
		for f := range dfBins {
			coefs[((dfLookback*n+t)*dfBins+f)*2] = 1
		}
	}
	return mask, coefs, nil
}

// speech is a gliding harmonic stack with syllabic swells, peaking near 0.3.
func speech(n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		t := float64(i) / sampleRate
		var v float64
		for k := 1; k <= 8; k++ {
			v += math.Sin(2*math.Pi*float64(k)*(140*t+0.3*math.Sin(2*math.Pi*0.7*t))) / float64(k)
		}
		out[i] = float32(0.12 * v * (0.3 + 0.7*math.Pow(math.Sin(2*math.Pi*2.7*t), 2)))
	}
	return out
}

// addClicks adds a 6 ms burst at each of at, in samples.
func addClicks(x []float32, at []int, seed uint64) {
	r := rand.New(rand.NewPCG(seed, seed)) //nolint:gosec // repeatable test noise
	for _, p := range at {
		for i := range 6 * sampleRate / 1000 {
			x[p+i] += float32(0.5 * math.Exp(-float64(i)/(0.0015*sampleRate)) * (2*r.Float64() - 1))
		}
	}
}

// keyTimes returns n key times between from and to with irregular gaps.
func keyTimes(n int, from, to time.Duration, seed uint64) []time.Duration {
	r := rand.New(rand.NewPCG(seed, 1)) //nolint:gosec // repeatable test timing
	times := make([]time.Duration, n)
	step := (to - from) / time.Duration(n)
	for i := range times {
		times[i] = from + time.Duration(i)*step + time.Duration(r.Float64()*0.6*float64(step))
	}
	return times
}

func samplesAt(times []time.Duration, lag time.Duration) []int {
	at := make([]int, len(times))
	for i, t := range times {
		at[i] = int((t + lag).Seconds() * sampleRate)
	}
	return at
}

func TestAlignFindsTheLag(t *testing.T) {
	keys := keyTimes(20, time.Second, 9*time.Second, 1)
	const lag = 237 * time.Millisecond
	x := speech(10 * sampleRate)
	addClicks(x, samplesAt(keys, lag), 2)
	var o onsets
	o.add(x)
	got, ok := align(keys, o.strength)
	if !ok {
		t.Fatal("the key times were not lined up with the clicks")
	}
	if d := got - lag; d < -10*time.Millisecond || d > 10*time.Millisecond {
		t.Fatalf("lag = %s, want %s", got, lag)
	}
}

func TestAlignRejectsUnrelatedTimes(t *testing.T) {
	x := speech(10 * sampleRate)
	addClicks(x, samplesAt(keyTimes(20, time.Second, 9*time.Second, 1), 0), 2)
	var o onsets
	o.add(x)
	if lag, ok := align(keyTimes(20, time.Second, 9*time.Second, 99), o.strength); ok {
		t.Fatalf("unrelated key times lined up at %s", lag)
	}
}

// runCleaner passes interleaved channels through c.
func runCleaner(t *testing.T, c *cleaner, channels [][]float32) [][]float32 {
	t.Helper()
	var in bytes.Buffer
	for i := range channels[0] {
		for _, ch := range channels {
			_ = binary.Write(&in, binary.LittleEndian, ch[i])
		}
	}
	var out bytes.Buffer
	if err := c.process(&in, &out, func(int64) {}); err != nil {
		t.Fatal(err)
	}
	got := make([][]float32, len(channels))
	raw := out.Bytes()
	for i := 0; 4*i < len(raw); i++ {
		got[i%len(channels)] = append(got[i%len(channels)], math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:])))
	}
	return got
}

func rms(x []float32, from, to int) float64 {
	var s float64
	for _, v := range x[from:to] {
		s += float64(v) * float64(v)
	}
	return math.Sqrt(s / float64(to-from))
}

func TestCleanerRepairsClicksAndLeavesTheRest(t *testing.T) {
	const length = 70 * sampleRate
	clean := speech(length)
	// Clicks either side of the 30 s segment boundary too.
	keys := append(keyTimes(12, time.Second, 29*time.Second, 3), 29970*time.Millisecond, 30020*time.Millisecond, 45*time.Second)
	at := samplesAt(keys, 0)
	left := append([]float32(nil), clean...)
	addClicks(left, at, 4)
	right := append([]float32(nil), left...)
	for i := range right {
		right[i] *= 0.5
	}
	// The logged times are a few milliseconds early, as the keyboard's
	// latency makes them.
	positions := make([]int64, len(at))
	for i, p := range at {
		positions[i] = int64(p - 4*sampleRate/1000)
	}

	got := runCleaner(t, &cleaner{channels: 2, run: identity, clicks: positions}, [][]float32{left, right})
	if len(got[0]) != length || len(got[1]) != length {
		t.Fatalf("got %d and %d samples, want %d", len(got[0]), len(got[1]), length)
	}
	near := make([]bool, length)
	for _, p := range at {
		for i := max(0, p-repairReach); i < min(length, p+repairReach); i++ {
			near[i] = true
		}
	}
	for ch, want := range [][]float32{left, right} {
		for i := range length {
			if !near[i] && math.Abs(float64(got[ch][i]-want[i])) > 1e-4 {
				t.Fatalf("channel %d sample %d away from any click changed from %f to %f", ch, i, want[i], got[ch][i])
			}
		}
	}
	for _, p := range at {
		from, to := p, p+10*sampleRate/1000
		noise := func(x []float32) float64 {
			d := make([]float32, to-from)
			for i := range d {
				d[i] = x[from+i] - clean[from+i]
			}
			return rms(d, 0, len(d))
		}
		before, after := noise(left), noise(got[0])
		if after > 0.4*before {
			t.Fatalf("click at %.3f s: %.4f left of %.4f, want at least 8 dB less", float64(p)/sampleRate, after, before)
		}
	}
}

func TestCleanerWithoutClicksKeepsTheSound(t *testing.T) {
	x := speech(31*sampleRate + 123)
	got := runCleaner(t, &cleaner{channels: 1, run: identity}, [][]float32{x})
	if len(got[0]) != len(x) {
		t.Fatalf("got %d samples, want %d", len(got[0]), len(x))
	}
	for i := range x {
		if math.Abs(float64(got[0][i]-x[i])) > 1e-4 {
			t.Fatalf("sample %d changed from %f to %f", i, x[i], got[0][i])
		}
	}
}
