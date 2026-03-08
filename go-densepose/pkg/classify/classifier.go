package classify

import (
	"math"

	"github.com/ruvnet/go-densepose/pkg/csi"
)

// Thresholds for motion classification.
type Thresholds struct {
	ActiveMotion       float64
	PresentMoving      float64
	PresentStill       float64
	PresenceVariance   float64
}

func DefaultThresholds() Thresholds {
	return Thresholds{
		ActiveMotion:     0.40,
		PresentMoving:    0.22,
		PresentStill:     0.08,
		PresenceVariance: 0.5,
	}
}

// PresenceClassifier classifies motion level with EMA smoothing and hysteresis.
type PresenceClassifier struct {
	thresholds   Thresholds
	emaAlpha     float64
	smoothScore  float64
	history      []csi.MotionLevel
	debounceLen  int
	initialized  bool
}

func NewPresenceClassifier(thresh Thresholds) *PresenceClassifier {
	return &PresenceClassifier{
		thresholds:  thresh,
		emaAlpha:    0.35,
		history:     make([]csi.MotionLevel, 0, 4),
		debounceLen: 2,
	}
}

func (c *PresenceClassifier) Classify(f *csi.SignalFeatures) csi.Classification {
	motionScore := c.computeMotionScore(f)

	if !c.initialized {
		c.smoothScore = motionScore
		c.initialized = true
	} else {
		c.smoothScore = c.emaAlpha*motionScore + (1-c.emaAlpha)*c.smoothScore
	}

	rawLevel := c.scoreToLevel(c.smoothScore)

	c.history = append(c.history, rawLevel)
	if len(c.history) > c.debounceLen {
		c.history = c.history[1:]
	}

	level := c.debounce()

	presence := level != csi.Absent
	conf := c.computeConfidence(f, c.smoothScore, presence)

	return csi.Classification{
		Motion:     level,
		Presence:   presence,
		Confidence: conf,
	}
}

func (c *PresenceClassifier) Reset() {
	c.smoothScore = 0
	c.initialized = false
	c.history = c.history[:0]
}

func (c *PresenceClassifier) computeMotionScore(f *csi.SignalFeatures) float64 {
	// Scale factors tuned for RSSI-based data (variance ~0.1-5, motPow ~0.5-30)
	varianceComponent := math.Min(1.0, f.Variance/3.0) * 0.30
	motionBand := math.Min(1.0, f.MotionBandPower/15.0) * 0.25
	spectral := math.Min(1.0, f.SpectralPower/50.0) * 0.10
	changePoints := math.Min(1.0, float64(f.ChangePoints)/8.0) * 0.10
	breathingBand := math.Min(1.0, f.BreathingBandPower/5.0) * 0.10
	stdComponent := math.Min(1.0, f.StdDev/3.0) * 0.15

	return varianceComponent + motionBand + spectral + changePoints + breathingBand + stdComponent
}

func (c *PresenceClassifier) scoreToLevel(score float64) csi.MotionLevel {
	t := c.thresholds
	switch {
	case score > t.ActiveMotion:
		return csi.Active
	case score > t.PresentMoving:
		return csi.PresentMoving
	case score > t.PresentStill:
		return csi.PresentStill
	default:
		return csi.Absent
	}
}

func (c *PresenceClassifier) debounce() csi.MotionLevel {
	if len(c.history) < c.debounceLen {
		return c.history[len(c.history)-1]
	}

	counts := make(map[csi.MotionLevel]int)
	for _, l := range c.history {
		counts[l]++
	}

	best := csi.Absent
	bestCount := 0
	for level, count := range counts {
		if count > bestCount {
			best = level
			bestCount = count
		}
	}
	return best
}

func (c *PresenceClassifier) computeConfidence(f *csi.SignalFeatures, score float64, present bool) float64 {
	if !present {
		return math.Max(0.3, math.Min(0.7, 1.0-score*3))
	}
	snr := f.MeanRSSI - (-95.0)
	snrConf := math.Min(1.0, math.Max(0.0, snr/40.0))
	scoreConf := math.Min(1.0, score*2)
	return math.Min(0.99, 0.4*snrConf+0.6*scoreConf)
}
