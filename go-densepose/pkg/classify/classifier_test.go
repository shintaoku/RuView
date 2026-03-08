package classify

import (
	"testing"

	"github.com/ruvnet/go-densepose/pkg/csi"
)

func TestClassifier_Absent(t *testing.T) {
	c := NewPresenceClassifier(DefaultThresholds())

	feat := &csi.SignalFeatures{
		MeanRSSI:        -80,
		Variance:        0.01,
		StdDev:          0.1,
		MotionBandPower: 0.001,
		SpectralPower:   0.5,
		ChangePoints:    0,
	}

	// Run multiple times to get past debounce
	var cls csi.Classification
	for i := 0; i < 10; i++ {
		cls = c.Classify(feat)
	}

	if cls.Motion != csi.Absent {
		t.Errorf("expected absent, got %s", cls.Motion)
	}
	if cls.Presence {
		t.Error("presence should be false for absent")
	}
}

func TestClassifier_Active(t *testing.T) {
	c := NewPresenceClassifier(DefaultThresholds())

	feat := &csi.SignalFeatures{
		MeanRSSI:           -40,
		Variance:           15.0,
		StdDev:             3.87,
		MotionBandPower:    8.0,
		BreathingBandPower: 2.0,
		SpectralPower:      200.0,
		ChangePoints:       8,
	}

	var cls csi.Classification
	for i := 0; i < 10; i++ {
		cls = c.Classify(feat)
	}

	if cls.Motion != csi.Active {
		t.Errorf("expected active, got %s", cls.Motion)
	}
	if !cls.Presence {
		t.Error("presence should be true for active")
	}
	if cls.Confidence < 0.5 {
		t.Errorf("confidence should be >= 0.5 for active, got %v", cls.Confidence)
	}
}

func TestClassifier_PresentStill(t *testing.T) {
	c := NewPresenceClassifier(DefaultThresholds())

	feat := &csi.SignalFeatures{
		MeanRSSI:           -50,
		Variance:           0.5,
		StdDev:             0.7,
		MotionBandPower:    0.15,
		BreathingBandPower: 0.1,
		SpectralPower:      3.0,
		ChangePoints:       0,
	}

	var cls csi.Classification
	for i := 0; i < 10; i++ {
		cls = c.Classify(feat)
	}

	if cls.Motion != csi.PresentStill {
		t.Errorf("expected present_still, got %s", cls.Motion)
	}
	if !cls.Presence {
		t.Error("presence should be true")
	}
}

func TestClassifier_Reset(t *testing.T) {
	c := NewPresenceClassifier(DefaultThresholds())

	feat := &csi.SignalFeatures{Variance: 20, MotionBandPower: 10, SpectralPower: 300}
	c.Classify(feat)
	c.Reset()

	// After reset, should behave like fresh classifier
	quiet := &csi.SignalFeatures{Variance: 0.01}
	cls := c.Classify(quiet)
	if cls.Motion != csi.Absent {
		t.Errorf("after reset with quiet signal, expected absent, got %s", cls.Motion)
	}
}

func TestClassifier_Confidence_Range(t *testing.T) {
	c := NewPresenceClassifier(DefaultThresholds())

	scenarios := []*csi.SignalFeatures{
		{Variance: 0},
		{Variance: 5, MotionBandPower: 3, SpectralPower: 50},
		{Variance: 20, MotionBandPower: 10, SpectralPower: 500, ChangePoints: 15},
	}

	for _, feat := range scenarios {
		cls := c.Classify(feat)
		if cls.Confidence < 0 || cls.Confidence > 1.0 {
			t.Errorf("confidence %v out of [0, 1] range", cls.Confidence)
		}
	}
}
