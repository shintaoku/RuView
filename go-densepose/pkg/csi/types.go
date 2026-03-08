package csi

import (
	"math"
	"math/cmplx"
	"time"

	"github.com/google/uuid"
)

const (
	MaxKeypoints    = 17
	MaxSubcarriers  = 256
	DefaultConfThreshold = 0.5
)

type FrequencyBand int

const (
	Band2_4GHz FrequencyBand = iota
	Band5GHz
	Band6GHz
)

func (b FrequencyBand) String() string {
	switch b {
	case Band2_4GHz:
		return "2.4GHz"
	case Band5GHz:
		return "5GHz"
	case Band6GHz:
		return "6GHz"
	default:
		return "unknown"
	}
}

type AntennaConfig struct {
	TxAntennas uint8  `json:"tx_antennas"`
	RxAntennas uint8  `json:"rx_antennas"`
	SpacingMM  uint16 `json:"spacing_mm,omitempty"`
}

type CsiMetadata struct {
	Timestamp      time.Time     `json:"timestamp"`
	DeviceID       string        `json:"device_id"`
	NodeID         uint8         `json:"node_id"`
	FreqBand       FrequencyBand `json:"frequency_band"`
	ChannelFreqMHz uint32        `json:"channel_freq_mhz"`
	BandwidthMHz   uint16        `json:"bandwidth_mhz"`
	Antennas       AntennaConfig `json:"antenna_config"`
	RssiDBm        int8          `json:"rssi_dbm"`
	NoiseFloorDBm  int8          `json:"noise_floor_dbm"`
	Sequence       uint32        `json:"sequence"`
	NSubcarriers   uint16        `json:"n_subcarriers"`
}

type SubcarrierData struct {
	Index int   `json:"index"`
	I     int16 `json:"i"`
	Q     int16 `json:"q"`
}

func (s SubcarrierData) Amplitude() float64 {
	return math.Sqrt(float64(s.I)*float64(s.I) + float64(s.Q)*float64(s.Q))
}

func (s SubcarrierData) Phase() float64 {
	return math.Atan2(float64(s.Q), float64(s.I))
}

func (s SubcarrierData) Complex() complex128 {
	return complex(float64(s.I), float64(s.Q))
}

type CsiFrame struct {
	ID          uuid.UUID        `json:"id"`
	Metadata    CsiMetadata      `json:"metadata"`
	Subcarriers []SubcarrierData `json:"-"`
	Amplitude   []float64        `json:"amplitude"`
	Phase       []float64        `json:"phase"`
}

func NewCsiFrame(meta CsiMetadata, subcarriers []SubcarrierData) *CsiFrame {
	f := &CsiFrame{
		ID:          uuid.New(),
		Metadata:    meta,
		Subcarriers: subcarriers,
		Amplitude:   make([]float64, len(subcarriers)),
		Phase:       make([]float64, len(subcarriers)),
	}
	for i, sc := range subcarriers {
		c := sc.Complex()
		f.Amplitude[i] = cmplx.Abs(c)
		f.Phase[i] = cmplx.Phase(c)
	}
	return f
}

func (f *CsiFrame) MeanAmplitude() float64 {
	if len(f.Amplitude) == 0 {
		return 0
	}
	sum := 0.0
	for _, a := range f.Amplitude {
		sum += a
	}
	return sum / float64(len(f.Amplitude))
}

func (f *CsiFrame) SNR() float64 {
	return float64(f.Metadata.RssiDBm) - float64(f.Metadata.NoiseFloorDBm)
}

// KeypointType represents the 17 COCO body keypoints.
type KeypointType int

const (
	Nose KeypointType = iota
	LeftEye
	RightEye
	LeftEar
	RightEar
	LeftShoulder
	RightShoulder
	LeftElbow
	RightElbow
	LeftWrist
	RightWrist
	LeftHip
	RightHip
	LeftKnee
	RightKnee
	LeftAnkle
	RightAnkle
)

var keypointNames = [MaxKeypoints]string{
	"nose", "left_eye", "right_eye", "left_ear", "right_ear",
	"left_shoulder", "right_shoulder", "left_elbow", "right_elbow",
	"left_wrist", "right_wrist", "left_hip", "right_hip",
	"left_knee", "right_knee", "left_ankle", "right_ankle",
}

func (k KeypointType) String() string {
	if int(k) < len(keypointNames) {
		return keypointNames[k]
	}
	return "unknown"
}

type Keypoint struct {
	Type       KeypointType `json:"type"`
	X          float64      `json:"x"`
	Y          float64      `json:"y"`
	Z          float64      `json:"z"`
	Confidence float64      `json:"confidence"`
}

type BoundingBox struct {
	XMin float64 `json:"x_min"`
	YMin float64 `json:"y_min"`
	XMax float64 `json:"x_max"`
	YMax float64 `json:"y_max"`
}

type PersonPose struct {
	ID         string      `json:"id"`
	Keypoints  []Keypoint  `json:"keypoints"`
	BBox       BoundingBox `json:"bounding_box"`
	Confidence float64     `json:"confidence"`
}

type PoseEstimate struct {
	ID           uuid.UUID    `json:"id"`
	Timestamp    time.Time    `json:"timestamp"`
	Persons      []PersonPose `json:"persons"`
	Confidence   float64      `json:"confidence"`
	LatencyMs    float64      `json:"latency_ms"`
	ModelVersion string       `json:"model_version"`
}

type VitalSigns struct {
	BreathingRateBPM     float64 `json:"breathing_rate_bpm"`
	HeartRateBPM         float64 `json:"heart_rate_bpm"`
	BreathingConfidence  float64 `json:"breathing_confidence"`
	HeartbeatConfidence  float64 `json:"heartbeat_confidence"`
	SignalQuality        float64 `json:"signal_quality"`
}

// MotionLevel classifies the current sensing state.
type MotionLevel string

const (
	Absent        MotionLevel = "absent"
	PresentStill  MotionLevel = "present_still"
	PresentMoving MotionLevel = "present_moving"
	Active        MotionLevel = "active"
)

type Classification struct {
	Motion     MotionLevel `json:"motion_level"`
	Presence   bool        `json:"presence"`
	Confidence float64     `json:"confidence"`
}

// SignalFeatures holds extracted features from CSI data.
type SignalFeatures struct {
	MeanRSSI           float64 `json:"mean_rssi"`
	Variance           float64 `json:"variance"`
	StdDev             float64 `json:"std"`
	MotionBandPower    float64 `json:"motion_band_power"`
	BreathingBandPower float64 `json:"breathing_band_power"`
	DominantFreqHz     float64 `json:"dominant_freq_hz"`
	ChangePoints       int     `json:"change_points"`
	SpectralPower      float64 `json:"spectral_power"`
	Range              float64 `json:"range"`
	IQR                float64 `json:"iqr"`
	Skewness           float64 `json:"skewness"`
	Kurtosis           float64 `json:"kurtosis"`
}

// SignalField represents the 2D signal-strength field for visualization.
type SignalField struct {
	GridSize [3]int    `json:"grid_size"`
	Values   []float64 `json:"values"`
}

// SensingUpdate is the main message broadcast via WebSocket.
type SensingUpdate struct {
	Type           string          `json:"type"`
	Timestamp      float64         `json:"timestamp"`
	Source         string          `json:"source"`
	Tick           uint64          `json:"tick"`
	Nodes          []NodeInfo      `json:"nodes"`
	Features       SignalFeatures  `json:"features"`
	Classification Classification  `json:"classification"`
	SignalField    *SignalField    `json:"signal_field,omitempty"`
	VitalSigns     *VitalSigns    `json:"vital_signs,omitempty"`
	Persons        []PersonPose   `json:"persons,omitempty"`
	EstimatedCount int            `json:"estimated_persons"`
}

type NodeInfo struct {
	NodeID          uint8     `json:"node_id"`
	RssiDBm         float64   `json:"rssi_dbm"`
	Position        [3]float64 `json:"position"`
	Amplitude       []float64  `json:"amplitude,omitempty"`
	SubcarrierCount int        `json:"subcarrier_count"`
}
