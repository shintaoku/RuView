package csi

// SignalProcessor processes CSI frames and produces features.
type SignalProcessor interface {
	PushFrame(frame *CsiFrame)
	Process() (*SignalFeatures, error)
	BufferedFrameCount() int
	Reset()
}

// NeuralInference runs pose estimation inference on processed signals.
type NeuralInference interface {
	Infer(features *SignalFeatures) (*PoseEstimate, error)
	LoadModel(path string) error
	ModelInfo() map[string]string
}

// Classifier determines motion level and presence from features.
type Classifier interface {
	Classify(features *SignalFeatures) Classification
	Reset()
}

// VitalDetector extracts breathing and heart rate from CSI data.
type VitalDetector interface {
	ProcessFrame(frame *CsiFrame) *VitalSigns
	Reset()
}

// DataSource represents any source of CSI frames (ESP32, WiFi, simulated).
type DataSource interface {
	Start() error
	Stop()
	Frames() <-chan *CsiFrame
	Source() string
}
