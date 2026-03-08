package vitals

import (
	"math"
	"testing"
	"time"

	"github.com/ruvnet/go-densepose/pkg/csi"
)

func breathingFrame(t float64, breathHz float64) *csi.CsiFrame {
	nSub := 16
	subs := make([]csi.SubcarrierData, nSub)
	for i := 0; i < nSub; i++ {
		baseAmp := 50.0
		breathSignal := 5.0 * math.Sin(2*math.Pi*breathHz*t)
		amp := baseAmp + breathSignal + float64(i)*0.1
		subs[i] = csi.SubcarrierData{
			Index: i,
			I:     int16(amp),
			Q:     int16(amp * 0.1),
		}
	}
	return csi.NewCsiFrame(csi.CsiMetadata{
		Timestamp:     time.Now(),
		RssiDBm:       -40,
		NoiseFloorDBm: -95,
		NSubcarriers:  uint16(nSub),
	}, subs)
}

func TestDetector_NeedsMinimumSamples(t *testing.T) {
	det := NewDetector(10.0)

	frame := breathingFrame(0, 0.25)
	vs := det.ProcessFrame(frame)

	if vs.BreathingRateBPM != 0 {
		t.Error("should not detect breathing with only 1 frame")
	}
}

func TestDetector_BreathingDetection(t *testing.T) {
	det := NewDetector(10.0)
	breathHz := 0.25 // 15 BPM

	// Feed 10 seconds of data at 10 Hz
	for i := 0; i < 100; i++ {
		tSec := float64(i) / 10.0
		frame := breathingFrame(tSec, breathHz)
		det.ProcessFrame(frame)
	}

	// Get the last result
	lastFrame := breathingFrame(10.0, breathHz)
	vs := det.ProcessFrame(lastFrame)

	if vs.BreathingRateBPM == 0 {
		t.Error("should detect breathing after 10 seconds")
	}

	// Expected: 0.25 Hz × 60 = 15 BPM, allow wide margin for Goertzel resolution
	expectedBPM := breathHz * 60.0
	if math.Abs(vs.BreathingRateBPM-expectedBPM) > 10 {
		t.Errorf("BreathingRateBPM = %.1f, expected ~%.1f (±10)", vs.BreathingRateBPM, expectedBPM)
	}
}

func TestDetector_Reset(t *testing.T) {
	det := NewDetector(10.0)

	for i := 0; i < 100; i++ {
		det.ProcessFrame(breathingFrame(float64(i)/10.0, 0.25))
	}

	det.Reset()

	vs := det.ProcessFrame(breathingFrame(0, 0.25))
	if vs.BreathingRateBPM != 0 {
		t.Error("after reset, should not have enough data")
	}
}

func TestDetector_SignalQuality(t *testing.T) {
	det := NewDetector(10.0)

	// Constant amplitude → high signal quality
	for i := 0; i < 20; i++ {
		nSub := 4
		subs := make([]csi.SubcarrierData, nSub)
		for j := 0; j < nSub; j++ {
			subs[j] = csi.SubcarrierData{I: 50, Q: 0}
		}
		frame := csi.NewCsiFrame(csi.CsiMetadata{NSubcarriers: uint16(nSub)}, subs)
		det.ProcessFrame(frame)
	}

	subs := make([]csi.SubcarrierData, 4)
	for j := 0; j < 4; j++ {
		subs[j] = csi.SubcarrierData{I: 50, Q: 0}
	}
	vs := det.ProcessFrame(csi.NewCsiFrame(csi.CsiMetadata{NSubcarriers: 4}, subs))
	if vs.SignalQuality < 0.8 {
		t.Errorf("constant signal should have high quality, got %v", vs.SignalQuality)
	}
}

func TestGoertzel_KnownFrequency(t *testing.T) {
	sampleRate := 100.0
	n := 1000
	sig := make([]float64, n)
	targetFreq := 5.0

	for i := 0; i < n; i++ {
		sig[i] = math.Sin(2 * math.Pi * targetFreq * float64(i) / sampleRate)
	}

	power5 := goertzel(sig, 5.0, sampleRate)
	power10 := goertzel(sig, 10.0, sampleRate)

	if power5 <= power10 {
		t.Errorf("power at target freq (%.1f) should be higher than off-target; got %.1f vs %.1f", targetFreq, power5, power10)
	}
}
