package wifi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ruvnet/go-densepose/pkg/csi"
)

// Scanner collects RSSI data from the host WiFi interface.
type Scanner struct {
	sampleRate float64
	frames     chan *csi.CsiFrame
	running    bool
	mu         sync.Mutex
	source     string
	sequence   uint32
}

func NewScanner(sampleRate float64, bufSize int) *Scanner {
	if sampleRate <= 0 {
		sampleRate = 10.0
	}
	return &Scanner{
		sampleRate: sampleRate,
		frames:     make(chan *csi.CsiFrame, bufSize),
	}
}

func (s *Scanner) Frames() <-chan *csi.CsiFrame { return s.frames }
func (s *Scanner) Source() string               { return s.source }

func (s *Scanner) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = true

	switch runtime.GOOS {
	case "darwin":
		s.source = "macos_wifi"
		go s.macOSLoop()
	case "linux":
		s.source = "linux_wifi"
		go s.linuxLoop()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	log.Printf("[wifi] scanner started on %s at %.1f Hz", runtime.GOOS, s.sampleRate)
	return nil
}

func (s *Scanner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
}

func (s *Scanner) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// macOSLoop uses CoreWLAN via inline Swift to stream RSSI at high frequency.
// This matches the approach used by RuView's Python v1 MacosWifiCollector.
func (s *Scanner) macOSLoop() {
	// First try: use the compiled mac_wifi binary from v1 if it exists
	swiftBin := s.findSwiftBinary()
	if swiftBin != "" {
		s.macOSStreamLoop(swiftBin)
		return
	}

	// Fallback: use inline swift -e with CoreWLAN (slower startup, but no build step)
	s.macOSInlineSwiftLoop()
}

func (s *Scanner) findSwiftBinary() string {
	candidates := []string{
		"v1/src/sensing/mac_wifi",
		"../v1/src/sensing/mac_wifi",
		"../../v1/src/sensing/mac_wifi",
	}
	for _, p := range candidates {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
		// Also try stat
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

// macOSStreamLoop reads JSON lines from a long-running Swift binary (same as RuView v1).
func (s *Scanner) macOSStreamLoop(binPath string) {
	log.Printf("[wifi] using Swift CoreWLAN binary: %s", binPath)
	cmd := exec.Command(binPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[wifi] failed to pipe stdout: %v, falling back to inline swift", err)
		s.macOSInlineSwiftLoop()
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("[wifi] failed to start %s: %v, falling back to inline swift", binPath, err)
		s.macOSInlineSwiftLoop()
		return
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if !s.isRunning() {
			return
		}
		line := scanner.Text()
		rssi, noise := parseSwiftJSON(line)
		frame := s.rssiToFrame(rssi, noise)
		select {
		case s.frames <- frame:
		default:
		}
	}
}

// macOSInlineSwiftLoop polls CoreWLAN via `swift -e` at the sample rate.
func (s *Scanner) macOSInlineSwiftLoop() {
	log.Println("[wifi] using inline Swift CoreWLAN (no mac_wifi binary found)")
	interval := time.Duration(float64(time.Second) / s.sampleRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	swiftCode := `import CoreWLAN; if let i=CWWiFiClient.shared().interface(){print("\(i.rssiValue()) \(i.noiseMeasurement())")}else{print("-70 -95")}`

	for range ticker.C {
		if !s.isRunning() {
			return
		}
		rssi, noise := s.readCoreWLAN(swiftCode)
		frame := s.rssiToFrame(rssi, noise)
		select {
		case s.frames <- frame:
		default:
		}
	}
}

func (s *Scanner) readCoreWLAN(swiftCode string) (rssi int8, noise int8) {
	rssi, noise = -70, -95
	cmd := exec.Command("swift", "-e", swiftCode)
	out, err := cmd.Output()
	if err != nil {
		return
	}
	var r, n int
	if _, err := fmt.Sscanf(string(out), "%d %d", &r, &n); err == nil {
		if r >= -128 && r <= 0 {
			rssi = int8(r)
		}
		if n >= -128 && n <= 0 {
			noise = int8(n)
		}
	}
	return
}

func parseSwiftJSON(line string) (rssi int8, noise int8) {
	rssi, noise = -70, -95
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return
	}
	var data struct {
		RSSI  float64 `json:"rssi"`
		Noise float64 `json:"noise"`
	}
	if err := json.Unmarshal([]byte(line), &data); err != nil {
		return
	}
	if data.RSSI >= -128 && data.RSSI <= 0 {
		rssi = int8(data.RSSI)
	}
	if data.Noise >= -128 && data.Noise <= 0 {
		noise = int8(data.Noise)
	}
	return
}

// linuxLoop reads /proc/net/wireless for RSSI.
func (s *Scanner) linuxLoop() {
	interval := time.Duration(float64(time.Second) / s.sampleRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		if !s.isRunning() {
			return
		}
		rssi, noise := s.readLinuxRSSI()
		frame := s.rssiToFrame(rssi, noise)
		select {
		case s.frames <- frame:
		default:
		}
	}
}

func (s *Scanner) readLinuxRSSI() (rssi int8, noise int8) {
	rssi, noise = -70, -95

	cmd := exec.Command("iw", "dev", "wlan0", "link")
	out, err := cmd.Output()
	if err != nil {
		return
	}

	lines := splitLines(string(out))
	for _, line := range lines {
		if val, ok := extractInt8(line, "signal:"); ok {
			rssi = val
		}
	}
	return
}

// rssiToFrame converts a single RSSI reading into a pseudo-CsiFrame
// with synthetic subcarrier data derived from the RSSI value.
func (s *Scanner) rssiToFrame(rssi, noise int8) *csi.CsiFrame {
	s.sequence++

	nSub := 16
	subcarriers := make([]csi.SubcarrierData, nSub)
	baseAmp := math.Max(1, float64(rssi+100))

	for i := 0; i < nSub; i++ {
		amp := baseAmp + math.Sin(float64(i)*0.5)*2.0
		phase := float64(i) * math.Pi / float64(nSub)
		subcarriers[i] = csi.SubcarrierData{
			Index: i,
			I:     int16(amp * math.Cos(phase)),
			Q:     int16(amp * math.Sin(phase)),
		}
	}

	meta := csi.CsiMetadata{
		Timestamp:      time.Now(),
		DeviceID:       "host-wifi",
		NodeID:         0,
		FreqBand:       csi.Band5GHz,
		ChannelFreqMHz: 5180,
		Antennas:       csi.AntennaConfig{TxAntennas: 1, RxAntennas: 1},
		RssiDBm:        rssi,
		NoiseFloorDBm:  noise,
		Sequence:       s.sequence,
		NSubcarriers:   uint16(nSub),
	}

	return csi.NewCsiFrame(meta, subcarriers)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func extractInt8(line, key string) (int8, bool) {
	for i := 0; i <= len(line)-len(key); i++ {
		if line[i:i+len(key)] == key {
			rest := line[i+len(key):]
			var val int
			_, err := fmt.Sscanf(rest, " %d", &val)
			if err == nil && val >= -128 && val <= 127 {
				return int8(val), true
			}
		}
	}
	return 0, false
}

// SimulatedSource generates synthetic CSI frames for testing.
type SimulatedSource struct {
	sampleRate float64
	frames     chan *csi.CsiFrame
	running    bool
	mu         sync.Mutex
}

func NewSimulatedSource(sampleRate float64, bufSize int) *SimulatedSource {
	return &SimulatedSource{
		sampleRate: sampleRate,
		frames:     make(chan *csi.CsiFrame, bufSize),
	}
}

func (s *SimulatedSource) Frames() <-chan *csi.CsiFrame { return s.frames }
func (s *SimulatedSource) Source() string               { return "simulated" }

func (s *SimulatedSource) Start() error {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	go s.loop()
	log.Printf("[simulated] source started at %.1f Hz", s.sampleRate)
	return nil
}

func (s *SimulatedSource) Stop() {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

func (s *SimulatedSource) loop() {
	interval := time.Duration(float64(time.Second) / s.sampleRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	seq := uint32(0)
	t0 := time.Now()

	for range ticker.C {
		s.mu.Lock()
		running := s.running
		s.mu.Unlock()
		if !running {
			return
		}

		seq++
		t := time.Since(t0).Seconds()
		nSub := 56

		subcarriers := make([]csi.SubcarrierData, nSub)
		for i := 0; i < nSub; i++ {
			amp := 10.0 + 3.0*math.Sin(2*math.Pi*0.3*t+float64(i)*0.1) +
				1.0*math.Sin(2*math.Pi*1.2*t+float64(i)*0.2)
			phase := math.Sin(2*math.Pi*0.05*t + float64(i)*math.Pi/float64(nSub))
			subcarriers[i] = csi.SubcarrierData{
				Index: i,
				I:     int16(amp * math.Cos(phase)),
				Q:     int16(amp * math.Sin(phase)),
			}
		}

		meta := csi.CsiMetadata{
			Timestamp:      time.Now(),
			DeviceID:       "simulated",
			NodeID:         1,
			FreqBand:       csi.Band2_4GHz,
			ChannelFreqMHz: 2412,
			Antennas:       csi.AntennaConfig{TxAntennas: 1, RxAntennas: 3},
			RssiDBm:        int8(-45 + int8(5*math.Sin(t*0.5))),
			NoiseFloorDBm:  -95,
			Sequence:       seq,
			NSubcarriers:   uint16(nSub),
		}

		frame := csi.NewCsiFrame(meta, subcarriers)
		select {
		case s.frames <- frame:
		default:
		}
	}
}

// Unused import guard
var _ = json.Marshal
