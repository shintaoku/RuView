package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ruvnet/go-densepose/pkg/csi"
)

func newTestServer(source string) (*Server, chan *csi.CsiFrame) {
	ch := make(chan *csi.CsiFrame, 100)
	cfg := DefaultConfig()
	cfg.TickMs = 50
	s := New(cfg, ch, source)
	return s, ch
}

func makeTestFrame(rssi int8, nSub int) *csi.CsiFrame {
	subs := make([]csi.SubcarrierData, nSub)
	for i := 0; i < nSub; i++ {
		subs[i] = csi.SubcarrierData{
			Index: i,
			I:     int16(10 + i),
			Q:     int16(5),
		}
	}
	return csi.NewCsiFrame(csi.CsiMetadata{
		Timestamp:     time.Now(),
		RssiDBm:       rssi,
		NoiseFloorDBm: -95,
		NSubcarriers:  uint16(nSub),
	}, subs)
}

func TestHealthEndpoint(t *testing.T) {
	s, _ := newTestServer("test_simulated")

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	s.handleHealth(w, req)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if data["status"] != "healthy" {
		t.Errorf("status = %v, want 'healthy'", data["status"])
	}
	if data["source"] != "test_simulated" {
		t.Errorf("source = %v, want 'test_simulated'", data["source"])
	}
	if data["lang"] != "go" {
		t.Errorf("lang = %v, want 'go'", data["lang"])
	}
}

func TestStatusEndpoint(t *testing.T) {
	s, _ := newTestServer("test_simulated")

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if data["source"] != "test_simulated" {
		t.Errorf("source = %v", data["source"])
	}
}

func TestLatestEndpoint_NoData(t *testing.T) {
	s, _ := newTestServer("test")

	req := httptest.NewRequest("GET", "/api/v1/sensing/latest", nil)
	w := httptest.NewRecorder()
	s.handleLatest(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "no data") {
		t.Errorf("expected 'no data' message, got: %s", body)
	}
}

func TestVitalSignsEndpoint_NoData(t *testing.T) {
	s, _ := newTestServer("test")

	req := httptest.NewRequest("GET", "/api/v1/vital-signs", nil)
	w := httptest.NewRecorder()
	s.handleVitalSigns(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "no vitals") {
		t.Errorf("expected 'no vitals' message, got: %s", body)
	}
}

func TestCORSMiddleware(t *testing.T) {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("OPTIONS", "/api/v1/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusNoContent {
		t.Errorf("OPTIONS should return 204, got %d", w.Result().StatusCode)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS origin header")
	}
}

func TestGenerateSignalField(t *testing.T) {
	feat := &csi.SignalFeatures{MotionBandPower: 2.0}
	cls := csi.Classification{Presence: true, Motion: csi.PresentMoving}

	field := generateSignalField(feat, cls)

	if field.GridSize[0] != 20 || field.GridSize[2] != 20 {
		t.Errorf("grid size = %v, want [20,1,20]", field.GridSize)
	}
	if len(field.Values) != 400 {
		t.Errorf("values length = %d, want 400", len(field.Values))
	}

	for i, v := range field.Values {
		if v < 0 || v > 1 {
			t.Errorf("value[%d] = %v, should be in [0, 1]", i, v)
			break
		}
	}
}

func TestWebSocketIntegration(t *testing.T) {
	s, frameCh := newTestServer("test_ws")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go s.processFrames(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/sensing", s.handleWebSocket)
	mux.HandleFunc("/health", s.handleHealth)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Feed frames to trigger processing
	for i := 0; i < 10; i++ {
		frameCh <- makeTestFrame(-50, 8)
	}

	time.Sleep(100 * time.Millisecond)

	// Connect WebSocket
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/sensing"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer ws.Close()

	// Push more frames and wait for broadcast
	for i := 0; i < 5; i++ {
		frameCh <- makeTestFrame(-45, 8)
	}

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ws read error: %v", err)
	}

	var update csi.SensingUpdate
	if err := json.Unmarshal(msg, &update); err != nil {
		t.Fatalf("invalid sensing update JSON: %v", err)
	}

	if update.Type != "sensing_update" {
		t.Errorf("type = %q, want 'sensing_update'", update.Type)
	}
	if update.Source != "test_ws" {
		t.Errorf("source = %q, want 'test_ws'", update.Source)
	}
	if update.Timestamp == 0 {
		t.Error("timestamp should not be zero")
	}
}

func TestIndexEndpoint(t *testing.T) {
	s, _ := newTestServer("test")

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "Go DensePose") {
		t.Error("index should contain 'Go DensePose'")
	}
}
