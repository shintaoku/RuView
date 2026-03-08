package vitals

import (
	"math"
	"sync"

	"github.com/ruvnet/go-densepose/pkg/csi"
)

const (
	breathingMinHz = 0.1
	breathingMaxHz = 0.5
	heartMinHz     = 0.667
	heartMaxHz     = 2.0
	defaultSampleRate = 10.0
)

// Detector extracts breathing and heart rate from CSI frame sequences.
type Detector struct {
	sampleRate   float64
	ampHistory   []float64
	phaseHistory []float64
	maxHistory   int
	mu           sync.Mutex
}

func NewDetector(sampleRate float64) *Detector {
	if sampleRate <= 0 {
		sampleRate = defaultSampleRate
	}
	maxHist := int(sampleRate * 30) // 30 seconds of history
	return &Detector{
		sampleRate: sampleRate,
		maxHistory: maxHist,
		ampHistory: make([]float64, 0, maxHist),
		phaseHistory: make([]float64, 0, maxHist),
	}
}

func (d *Detector) ProcessFrame(frame *csi.CsiFrame) *csi.VitalSigns {
	d.mu.Lock()
	defer d.mu.Unlock()

	meanAmp := frame.MeanAmplitude()
	d.ampHistory = append(d.ampHistory, meanAmp)
	if len(d.ampHistory) > d.maxHistory {
		d.ampHistory = d.ampHistory[1:]
	}

	if len(frame.Phase) > 0 {
		meanPhase := mean(frame.Phase)
		d.phaseHistory = append(d.phaseHistory, meanPhase)
		if len(d.phaseHistory) > d.maxHistory {
			d.phaseHistory = d.phaseHistory[1:]
		}
	}

	vs := &csi.VitalSigns{SignalQuality: d.signalQuality()}

	if len(d.ampHistory) < int(d.sampleRate*5) {
		return vs
	}

	// Breathing: Goertzel filter on amplitude in 0.1-0.5 Hz
	brBPM, brConf := d.detectFrequency(d.ampHistory, breathingMinHz, breathingMaxHz)
	vs.BreathingRateBPM = brBPM * 60.0
	vs.BreathingConfidence = brConf

	// Heart rate: Goertzel filter on phase variance in 0.667-2.0 Hz
	if len(d.phaseHistory) >= int(d.sampleRate*5) {
		hrBPM, hrConf := d.detectFrequency(d.phaseHistory, heartMinHz, heartMaxHz)
		vs.HeartRateBPM = hrBPM * 60.0
		vs.HeartbeatConfidence = hrConf
	}

	return vs
}

func (d *Detector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ampHistory = d.ampHistory[:0]
	d.phaseHistory = d.phaseHistory[:0]
}

// detectFrequency uses the Goertzel algorithm to find the dominant frequency
// in the given band.
func (d *Detector) detectFrequency(signal []float64, minHz, maxHz float64) (freqHz, confidence float64) {
	n := len(signal)
	if n < 10 {
		return 0, 0
	}

	// Remove DC component
	m := mean(signal)
	detrended := make([]float64, n)
	for i, v := range signal {
		detrended[i] = v - m
	}

	freqStep := 0.01
	bestFreq := 0.0
	bestPower := 0.0
	totalPower := 0.0

	for freq := minHz; freq <= maxHz; freq += freqStep {
		power := goertzel(detrended, freq, d.sampleRate)
		totalPower += power
		if power > bestPower {
			bestPower = power
			bestFreq = freq
		}
	}

	if totalPower == 0 {
		return 0, 0
	}

	nFreqs := int((maxHz - minHz) / freqStep)
	avgPower := totalPower / float64(max(nFreqs, 1))

	snr := bestPower / math.Max(avgPower, 1e-10)
	conf := math.Min(1.0, math.Max(0.0, (snr-1.0)/10.0))

	return bestFreq, conf
}

func (d *Detector) signalQuality() float64 {
	if len(d.ampHistory) < 10 {
		return 0
	}
	recent := d.ampHistory[len(d.ampHistory)-10:]
	m := mean(recent)
	if m == 0 {
		return 0
	}
	cv := stddev(recent) / m
	return math.Max(0, math.Min(1.0, 1.0-cv))
}

// goertzel computes the power at a specific frequency using the Goertzel algorithm.
func goertzel(signal []float64, targetFreq, sampleRate float64) float64 {
	n := len(signal)
	k := int(0.5 + float64(n)*targetFreq/sampleRate)
	w := 2.0 * math.Pi * float64(k) / float64(n)
	coeff := 2.0 * math.Cos(w)

	s0, s1, s2 := 0.0, 0.0, 0.0
	for _, x := range signal {
		s0 = x + coeff*s1 - s2
		s2 = s1
		s1 = s0
	}

	power := s1*s1 + s2*s2 - coeff*s1*s2
	return math.Abs(power)
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range vals {
		s += v
	}
	return s / float64(len(vals))
}

func stddev(vals []float64) float64 {
	if len(vals) < 2 {
		return 0
	}
	m := mean(vals)
	s := 0.0
	for _, v := range vals {
		d := v - m
		s += d * d
	}
	return math.Sqrt(s / float64(len(vals)-1))
}
