package features

import (
	"math"
	"testing"
	"time"

	"github.com/ruvnet/go-densepose/pkg/csi"
)

func makeFrame(rssi int8, amplitudes ...float64) *csi.CsiFrame {
	subs := make([]csi.SubcarrierData, len(amplitudes))
	for i, amp := range amplitudes {
		subs[i] = csi.SubcarrierData{
			Index: i,
			I:     int16(amp),
			Q:     0,
		}
	}
	return csi.NewCsiFrame(csi.CsiMetadata{
		Timestamp:     time.Now(),
		RssiDBm:       rssi,
		NoiseFloorDBm: -95,
		NSubcarriers:  uint16(len(amplitudes)),
	}, subs)
}

func TestExtractor_Empty(t *testing.T) {
	ext := NewExtractor(100)
	feat := ext.Extract()
	if feat.MeanRSSI != 0 {
		t.Errorf("empty extractor should return zero features, got MeanRSSI=%v", feat.MeanRSSI)
	}
}

func TestExtractor_SingleFrame(t *testing.T) {
	ext := NewExtractor(100)
	ext.PushFrame(makeFrame(-50, 10, 20, 30, 40))

	feat := ext.Extract()
	if feat.MeanRSSI != -50 {
		t.Errorf("MeanRSSI = %v, want -50", feat.MeanRSSI)
	}
	if feat.SpectralPower <= 0 {
		t.Error("SpectralPower should be positive")
	}
}

func TestExtractor_MultipleFrames_Variance(t *testing.T) {
	ext := NewExtractor(100)

	// Push frames with different amplitudes to create variance
	ext.PushFrame(makeFrame(-50, 10, 10, 10, 10))
	ext.PushFrame(makeFrame(-50, 20, 20, 20, 20))
	ext.PushFrame(makeFrame(-50, 10, 10, 10, 10))
	ext.PushFrame(makeFrame(-50, 20, 20, 20, 20))

	feat := ext.Extract()
	if feat.Variance <= 0 {
		t.Error("variance should be > 0 with varying frames")
	}
}

func TestExtractor_ChangePoints(t *testing.T) {
	ext := NewExtractor(100)
	// Frame with abrupt amplitude changes
	ext.PushFrame(makeFrame(-45, 5, 50, 5, 50, 5, 50, 5, 50))

	feat := ext.Extract()
	if feat.ChangePoints == 0 {
		t.Error("expected change points in alternating signal")
	}
}

func TestExtractor_BufferLimit(t *testing.T) {
	ext := NewExtractor(5)
	for i := 0; i < 10; i++ {
		ext.PushFrame(makeFrame(-50, 10, 20))
	}
	if ext.BufferedFrameCount() != 5 {
		t.Errorf("BufferedFrameCount = %d, want 5", ext.BufferedFrameCount())
	}
}

func TestExtractor_Reset(t *testing.T) {
	ext := NewExtractor(100)
	ext.PushFrame(makeFrame(-50, 10, 20))
	ext.Reset()
	if ext.BufferedFrameCount() != 0 {
		t.Error("buffer should be empty after reset")
	}
}

func TestStatistical_Mean(t *testing.T) {
	vals := []float64{2, 4, 6, 8, 10}
	got := mean(vals)
	if got != 6.0 {
		t.Errorf("mean = %v, want 6.0", got)
	}
}

func TestStatistical_Variance(t *testing.T) {
	vals := []float64{2, 4, 6, 8, 10}
	m := mean(vals)
	v := variance(vals, m)
	if math.Abs(v-10.0) > 0.001 {
		t.Errorf("variance = %v, want 10.0", v)
	}
}

func TestStatistical_Skewness_Symmetric(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5}
	m := mean(vals)
	s := skewness(vals, m)
	if math.Abs(s) > 0.01 {
		t.Errorf("symmetric distribution should have ~0 skewness, got %v", s)
	}
}
