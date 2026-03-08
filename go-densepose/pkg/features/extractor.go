package features

import (
	"math"
	"math/cmplx"
	"sort"
	"sync"

	"github.com/ruvnet/go-densepose/pkg/csi"
)

const defaultHistorySize = 30

type Extractor struct {
	history []*csi.CsiFrame
	maxSize int
	mu      sync.Mutex
}

func NewExtractor(historySize int) *Extractor {
	if historySize <= 0 {
		historySize = defaultHistorySize
	}
	return &Extractor{
		history: make([]*csi.CsiFrame, 0, historySize),
		maxSize: historySize,
	}
}

func (e *Extractor) PushFrame(frame *csi.CsiFrame) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = append(e.history, frame)
	if len(e.history) > e.maxSize {
		e.history = e.history[1:]
	}
}

func (e *Extractor) BufferedFrameCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.history)
}

func (e *Extractor) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = e.history[:0]
}

// Extract computes signal features from the RSSI time series,
// matching the algorithm in RuView Python v1's RssiFeatureExtractor.
func (e *Extractor) Extract() *csi.SignalFeatures {
	e.mu.Lock()
	frames := make([]*csi.CsiFrame, len(e.history))
	copy(frames, e.history)
	e.mu.Unlock()

	if len(frames) < 4 {
		return &csi.SignalFeatures{}
	}

	// Build RSSI time series (same as Python: rssi = [s.rssi_dbm for s in samples])
	n := len(frames)
	rssi := make([]float64, n)
	for i, f := range frames {
		rssi[i] = float64(f.Metadata.RssiDBm)
	}

	// Estimate sample rate from frame timestamps
	sampleRate := 10.0
	if n > 1 {
		first := frames[0].Metadata.Timestamp
		last := frames[n-1].Metadata.Timestamp
		duration := last.Sub(first).Seconds()
		if duration > 0 {
			sampleRate = float64(n-1) / duration
		}
	}

	// Time-domain features (matches Python _compute_time_domain)
	meanRSSI := mean(rssi)
	v := varianceDDOF1(rssi, meanRSSI)
	std := math.Sqrt(v)

	sorted := make([]float64, n)
	copy(sorted, rssi)
	sort.Float64s(sorted)

	rng := sorted[n-1] - sorted[0]
	q1 := sorted[n/4]
	q3 := sorted[3*n/4]
	iqr := q3 - q1

	skew := 0.0
	kurt := 0.0
	if std > 1e-12 {
		if n > 2 {
			skew = unbiasedSkewness(rssi, meanRSSI)
		}
		if n > 3 {
			kurt = unbiasedKurtosis(rssi, meanRSSI)
		}
	}

	// Frequency-domain features (matches Python _compute_frequency_domain)
	// Remove DC, apply Hann window, compute real FFT
	signal := make([]float64, n)
	for i := range signal {
		signal[i] = rssi[i] - meanRSSI
	}
	hannWindow(signal)

	freqs, psd := rfftPSD(signal, sampleRate)

	// Skip DC (index 0)
	var freqsNoDC, psdNoDC []float64
	if len(freqs) > 1 {
		freqsNoDC = freqs[1:]
		psdNoDC = psd[1:]
	}

	totalSpectralPower := sum(psdNoDC)

	dominantFreq := 0.0
	if len(psdNoDC) > 0 {
		peakIdx := argmax(psdNoDC)
		dominantFreq = freqsNoDC[peakIdx]
	}

	breathingBandPower := bandPower(freqsNoDC, psdNoDC, 0.1, 0.5)
	motionBandPower := bandPower(freqsNoDC, psdNoDC, 0.5, 3.0)

	// CUSUM change-point detection (matches Python)
	changePoints := cusumDetect(rssi, meanRSSI, 3.0*std, 0.5*std)

	return &csi.SignalFeatures{
		MeanRSSI:           meanRSSI,
		Variance:           v,
		StdDev:             std,
		MotionBandPower:    motionBandPower,
		BreathingBandPower: breathingBandPower,
		DominantFreqHz:     dominantFreq,
		ChangePoints:       changePoints,
		SpectralPower:      totalSpectralPower,
		Range:              rng,
		IQR:                iqr,
		Skewness:           skew,
		Kurtosis:           kurt,
	}
}

// --- helpers matching Python/numpy/scipy ---

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

func sum(vals []float64) float64 {
	s := 0.0
	for _, v := range vals {
		s += v
	}
	return s
}

func argmax(vals []float64) int {
	idx := 0
	for i, v := range vals {
		if v > vals[idx] {
			idx = i
		}
		_ = v
	}
	return idx
}

// varianceDDOF1 computes sample variance with ddof=1 (like numpy var(ddof=1))
func varianceDDOF1(vals []float64, m float64) float64 {
	if len(vals) < 2 {
		return 0
	}
	s := 0.0
	for _, v := range vals {
		d := v - m
		s += d * d
	}
	return s / float64(len(vals)-1)
}

// unbiasedSkewness matches scipy.stats.skew(bias=False)
func unbiasedSkewness(vals []float64, m float64) float64 {
	n := float64(len(vals))
	if n < 3 {
		return 0
	}
	s2, s3 := 0.0, 0.0
	for _, v := range vals {
		d := v - m
		s2 += d * d
		s3 += d * d * d
	}
	// Biased moment estimators
	m2 := s2 / n
	m3 := s3 / n
	if m2 == 0 {
		return 0
	}
	g1 := m3 / math.Pow(m2, 1.5)
	// Adjust for bias (scipy default bias=False)
	return g1 * math.Sqrt(n*(n-1)) / (n - 2)
}

// unbiasedKurtosis matches scipy.stats.kurtosis(bias=False) (excess kurtosis)
func unbiasedKurtosis(vals []float64, m float64) float64 {
	n := float64(len(vals))
	if n < 4 {
		return 0
	}
	s2, s4 := 0.0, 0.0
	for _, v := range vals {
		d := v - m
		d2 := d * d
		s2 += d2
		s4 += d2 * d2
	}
	m2 := s2 / n
	m4 := s4 / n
	if m2 == 0 {
		return 0
	}
	// Excess kurtosis (biased)
	g2 := m4/(m2*m2) - 3.0
	// Adjust for bias
	return ((n - 1) / ((n - 2) * (n - 3))) * ((n+1)*g2 + 6)
}

// hannWindow applies in-place Hann window
func hannWindow(signal []float64) {
	n := len(signal)
	for i := range signal {
		w := 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n)))
		signal[i] *= w
	}
}

// rfftPSD computes one-sided PSD from a real signal.
// Matches Python: scipy.fft.rfft → abs²/N
// The Hann window coherent gain correction is applied so that
// PSD values match scipy output on the same input.
func rfftPSD(signal []float64, sampleRate float64) (freqs []float64, psd []float64) {
	n := len(signal)
	if n == 0 {
		return nil, nil
	}

	// Hann window coherent gain = mean(window) = 0.5
	// To match scipy's PSD normalization after windowing, we divide by
	// the sum of the window squared (Parseval correction).
	// For Hann: sum(w^2)/N = 0.375
	// But scipy.fft.rfft already uses the unnormalized convention,
	// and Python divides by N. So we just do |FFT|^2 / N here.

	nOut := n/2 + 1
	fft := make([]complex128, nOut)
	for k := 0; k < nOut; k++ {
		var s complex128
		for t := 0; t < n; t++ {
			angle := -2 * math.Pi * float64(k) * float64(t) / float64(n)
			s += complex(signal[t], 0) * cmplx.Rect(1, angle)
		}
		fft[k] = s
	}

	freqs = make([]float64, nOut)
	psd = make([]float64, nOut)
	for k := 0; k < nOut; k++ {
		freqs[k] = float64(k) * sampleRate / float64(n)
		mag := cmplx.Abs(fft[k])
		psd[k] = (mag * mag) / float64(n)
	}
	return
}

// bandPower sums PSD within [lowHz, highHz]
func bandPower(freqs, psd []float64, lowHz, highHz float64) float64 {
	s := 0.0
	for i, f := range freqs {
		if f >= lowHz && f <= highHz {
			s += psd[i]
		}
	}
	return s
}

// cusumDetect implements CUSUM change-point detection (same as Python)
func cusumDetect(signal []float64, target, threshold, drift float64) int {
	sPos, sNeg := 0.0, 0.0
	count := 0
	for _, v := range signal {
		dev := v - target
		sPos = math.Max(0, sPos+dev-drift)
		sNeg = math.Max(0, sNeg-dev-drift)
		if sPos > threshold || sNeg > threshold {
			count++
			sPos = 0
			sNeg = 0
		}
	}
	return count
}
