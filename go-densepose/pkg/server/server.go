package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ruvnet/go-densepose/pkg/classify"
	"github.com/ruvnet/go-densepose/pkg/csi"
	"github.com/ruvnet/go-densepose/pkg/features"
	"github.com/ruvnet/go-densepose/pkg/vitals"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Config holds server configuration.
type Config struct {
	HTTPPort   int
	WSPort     int
	UIPath     string
	TickMs     int
	Source     string
}

func DefaultConfig() Config {
	return Config{
		HTTPPort: 3000,
		WSPort:   3001,
		UIPath:   "",
		TickMs:   100,
		Source:   "auto",
	}
}

// Server is the main sensing server.
type Server struct {
	config     Config
	extractor  *features.Extractor
	classifier *classify.PresenceClassifier
	vitalDet   *vitals.Detector
	clients    map[*websocket.Conn]bool
	clientsMu  sync.RWMutex
	latest     *csi.SensingUpdate
	latestMu   sync.RWMutex
	frameCh    <-chan *csi.CsiFrame
	source     string
	tick       uint64
	startTime  time.Time
	lastVitals *csi.VitalSigns
}

func New(cfg Config, frameCh <-chan *csi.CsiFrame, source string) *Server {
	return &Server{
		config:     cfg,
		extractor:  features.NewExtractor(30),
		classifier: classify.NewPresenceClassifier(classify.DefaultThresholds()),
		vitalDet:   vitals.NewDetector(10.0),
		clients:    make(map[*websocket.Conn]bool),
		frameCh:    frameCh,
		source:     source,
		startTime:  time.Now(),
	}
}

func (s *Server) Run(ctx context.Context) error {
	go s.processFrames(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/v1/status", s.handleStatus)
	mux.HandleFunc("/api/v1/sensing/latest", s.handleLatest)
	mux.HandleFunc("/api/v1/vital-signs", s.handleVitalSigns)
	mux.HandleFunc("/ws/sensing", s.handleWebSocket)

	if s.config.UIPath != "" {
		fs := http.FileServer(http.Dir(s.config.UIPath))
		mux.Handle("/ui/", http.StripPrefix("/ui/", fs))
	}

	addr := fmt.Sprintf(":%d", s.config.HTTPPort)
	log.Printf("[server] Go DensePose Sensing Server")
	log.Printf("[server]   HTTP:      http://localhost:%d", s.config.HTTPPort)
	log.Printf("[server]   WebSocket: ws://localhost:%d/ws/sensing", s.config.HTTPPort)
	log.Printf("[server]   Source:    %s", s.source)

	srv := &http.Server{Addr: addr, Handler: corsMiddleware(mux)}

	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	return srv.ListenAndServe()
}

func (s *Server) processFrames(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.config.TickMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-s.frameCh:
			if frame == nil {
				continue
			}
			s.extractor.PushFrame(frame)
			s.lastVitals = s.vitalDet.ProcessFrame(frame)
		case <-ticker.C:
			s.tick++
			if s.extractor.BufferedFrameCount() < 4 {
				continue
			}

			feat := s.extractor.Extract()
			cls := s.classifier.Classify(feat)
			vs := s.lastVitals

			update := &csi.SensingUpdate{
				Type:           "sensing_update",
				Timestamp:      float64(time.Now().UnixMilli()) / 1000.0,
				Source:         s.source,
				Tick:           s.tick,
				Features:       *feat,
				Classification: cls,
				SignalField:    generateSignalField(feat, cls),
			}

			if vs != nil && vs.BreathingConfidence > 0.1 {
				update.VitalSigns = vs
			}

			s.latestMu.Lock()
			s.latest = update
			s.latestMu.Unlock()

			s.broadcast(update)
		}
	}
}

func (s *Server) broadcast(update *csi.SensingUpdate) {
	data, err := json.Marshal(update)
	if err != nil {
		return
	}

	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for conn := range s.clients {
		err := conn.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			conn.Close()
			go func(c *websocket.Conn) {
				s.clientsMu.Lock()
				delete(s.clients, c)
				s.clientsMu.Unlock()
			}(conn)
		}
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}

	s.clientsMu.Lock()
	s.clients[conn] = true
	s.clientsMu.Unlock()

	log.Printf("[ws] client connected: %s", conn.RemoteAddr())

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			s.clientsMu.Lock()
			delete(s.clients, conn)
			s.clientsMu.Unlock()
			conn.Close()
			log.Printf("[ws] client disconnected: %s", conn.RemoteAddr())
			return
		}
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<html><body>
<h1>Go DensePose Sensing Server</h1>
<p>Go + gorilla/websocket</p>
<ul>
<li><a href="/health">/health</a></li>
<li><a href="/api/v1/status">/api/v1/status</a></li>
<li><a href="/api/v1/sensing/latest">/api/v1/sensing/latest</a></li>
<li><a href="/api/v1/vital-signs">/api/v1/vital-signs</a></li>
<li>ws://localhost:%d/ws/sensing</li>
</ul></body></html>`, s.config.HTTPPort)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"status":   "ok",
		"uptime_s": time.Since(s.startTime).Seconds(),
		"source":   s.source,
		"lang":     "go",
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.clientsMu.RLock()
	nClients := len(s.clients)
	s.clientsMu.RUnlock()

	writeJSON(w, map[string]any{
		"source":      s.source,
		"uptime_s":    time.Since(s.startTime).Seconds(),
		"tick":        s.tick,
		"ws_clients":  nClients,
		"frames":      s.extractor.BufferedFrameCount(),
	})
}

func (s *Server) handleLatest(w http.ResponseWriter, r *http.Request) {
	s.latestMu.RLock()
	latest := s.latest
	s.latestMu.RUnlock()

	if latest == nil {
		writeJSON(w, map[string]string{"status": "no data yet"})
		return
	}
	writeJSON(w, latest)
}

func (s *Server) handleVitalSigns(w http.ResponseWriter, r *http.Request) {
	s.latestMu.RLock()
	latest := s.latest
	s.latestMu.RUnlock()

	if latest == nil || latest.VitalSigns == nil {
		writeJSON(w, map[string]string{"status": "no vitals available"})
		return
	}
	writeJSON(w, latest.VitalSigns)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func generateSignalField(feat *csi.SignalFeatures, cls csi.Classification) *csi.SignalField {
	gridSize := 20
	values := make([]float64, gridSize*gridSize)
	t := float64(time.Now().UnixMilli()) / 1000.0
	cx, cy := float64(gridSize)/2, float64(gridSize)/2

	// Seeded noise floor (same approach as RuView Python v1)
	seed := int64(math.Abs(feat.MeanRSSI*100)) % (1 << 31)
	rng := newLCG(seed)

	for y := 0; y < gridSize; y++ {
		for x := 0; x < gridSize; x++ {
			// Noise floor
			v := 0.02 + rng.Float64()*0.06

			// Radial attenuation from base point (center)
			dist := math.Sqrt(math.Pow(float64(x)-cx, 2) + math.Pow(float64(y)-cy, 2))
			v += math.Max(0, 1-dist/(float64(gridSize)*0.7)) * 0.3

			if cls.Presence {
				// Body blob — same formula as RuView Python v1
				bx := cx + 3*math.Sin(t*0.2)
				by := cy + 2*math.Cos(t*0.15)
				sigma := 2.0 + feat.Variance*0.5
				dx := float64(x) - bx
				dy := float64(y) - by
				blob := math.Exp(-(dx*dx + dy*dy) / (2.0 * sigma * sigma))
				intensity := 0.3 + 0.7*math.Min(1.0, feat.MotionBandPower*5)
				v += blob * intensity

				// Breathing ring
				if feat.BreathingBandPower > 0.01 {
					breathPhase := math.Sin(2 * math.Pi * 0.3 * t)
					breathR := 3.0 + breathPhase*0.8
					distBody := math.Sqrt(dx*dx + dy*dy)
					ring := math.Exp(-math.Pow(distBody-breathR, 2) / 1.5)
					v += ring * feat.BreathingBandPower * 2
				}
			}

			values[y*gridSize+x] = math.Min(1, math.Max(0, v))
		}
	}

	return &csi.SignalField{
		GridSize: [3]int{gridSize, 1, gridSize},
		Values:   values,
	}
}

// Simple LCG random for deterministic noise (matches numpy seeded rng behavior)
type lcg struct{ state int64 }

func newLCG(seed int64) *lcg { return &lcg{state: seed} }
func (l *lcg) Float64() float64 {
	l.state = (l.state*6364136223846793005 + 1442695040888963407) & 0x7fffffffffffffff
	return float64(l.state) / float64(0x7fffffffffffffff)
}
