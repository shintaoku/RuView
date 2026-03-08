package csi

import (
	"math"
	"testing"
	"time"
)

func TestSubcarrierData_Amplitude(t *testing.T) {
	tests := []struct {
		name string
		sc   SubcarrierData
		want float64
	}{
		{"zero", SubcarrierData{I: 0, Q: 0}, 0},
		{"i only", SubcarrierData{I: 3, Q: 0}, 3},
		{"q only", SubcarrierData{I: 0, Q: 4}, 4},
		{"3-4-5", SubcarrierData{I: 3, Q: 4}, 5},
		{"negative", SubcarrierData{I: -3, Q: -4}, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.sc.Amplitude()
			if math.Abs(got-tt.want) > 0.001 {
				t.Errorf("Amplitude() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSubcarrierData_Phase(t *testing.T) {
	sc := SubcarrierData{I: 1, Q: 1}
	got := sc.Phase()
	want := math.Pi / 4
	if math.Abs(got-want) > 0.001 {
		t.Errorf("Phase() = %v, want %v", got, want)
	}
}

func TestNewCsiFrame(t *testing.T) {
	subs := []SubcarrierData{
		{Index: 0, I: 3, Q: 4},
		{Index: 1, I: 5, Q: 12},
		{Index: 2, I: 8, Q: 6},
	}
	meta := CsiMetadata{
		Timestamp:      time.Now(),
		NodeID:         1,
		RssiDBm:        -45,
		NoiseFloorDBm:  -95,
		NSubcarriers:   3,
		ChannelFreqMHz: 2412,
	}

	frame := NewCsiFrame(meta, subs)

	if frame.ID.String() == "" {
		t.Error("frame ID should not be empty")
	}
	if len(frame.Amplitude) != 3 {
		t.Errorf("expected 3 amplitudes, got %d", len(frame.Amplitude))
	}
	if len(frame.Phase) != 3 {
		t.Errorf("expected 3 phases, got %d", len(frame.Phase))
	}

	// Amplitude[0] should be sqrt(9+16) = 5
	if math.Abs(frame.Amplitude[0]-5.0) > 0.001 {
		t.Errorf("Amplitude[0] = %v, want 5.0", frame.Amplitude[0])
	}
	// Amplitude[1] should be sqrt(25+144) = 13
	if math.Abs(frame.Amplitude[1]-13.0) > 0.001 {
		t.Errorf("Amplitude[1] = %v, want 13.0", frame.Amplitude[1])
	}
}

func TestCsiFrame_MeanAmplitude(t *testing.T) {
	subs := []SubcarrierData{
		{I: 3, Q: 4},   // amp=5
		{I: 5, Q: 12},  // amp=13
		{I: 8, Q: 6},   // amp=10
	}
	frame := NewCsiFrame(CsiMetadata{}, subs)
	got := frame.MeanAmplitude()
	want := (5.0 + 13.0 + 10.0) / 3.0
	if math.Abs(got-want) > 0.001 {
		t.Errorf("MeanAmplitude() = %v, want %v", got, want)
	}
}

func TestCsiFrame_MeanAmplitude_Empty(t *testing.T) {
	frame := NewCsiFrame(CsiMetadata{}, nil)
	if frame.MeanAmplitude() != 0 {
		t.Error("empty frame should return 0")
	}
}

func TestCsiFrame_SNR(t *testing.T) {
	frame := NewCsiFrame(CsiMetadata{RssiDBm: -45, NoiseFloorDBm: -95}, nil)
	got := frame.SNR()
	if got != 50.0 {
		t.Errorf("SNR() = %v, want 50", got)
	}
}

func TestKeypointType_String(t *testing.T) {
	if Nose.String() != "nose" {
		t.Errorf("Nose.String() = %q", Nose.String())
	}
	if RightAnkle.String() != "right_ankle" {
		t.Errorf("RightAnkle.String() = %q", RightAnkle.String())
	}
	if KeypointType(99).String() != "unknown" {
		t.Error("out-of-range should be unknown")
	}
}

func TestMotionLevel_Values(t *testing.T) {
	if Absent != "absent" {
		t.Error("Absent mismatch")
	}
	if Active != "active" {
		t.Error("Active mismatch")
	}
}
