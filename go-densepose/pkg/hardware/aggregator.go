package hardware

import (
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/ruvnet/go-densepose/pkg/csi"
)

// Aggregator receives ESP32 CSI frames over UDP and dispatches them.
type Aggregator struct {
	bindAddr string
	port     int
	conn     *net.UDPConn
	frames   chan *csi.CsiFrame
	vitals   chan *EdgeVitals
	running  bool
	mu       sync.Mutex

	nodeStats map[uint8]*NodeStats
	statsMu   sync.RWMutex
}

type NodeStats struct {
	FramesReceived uint64
	LastSequence   uint32
	DroppedFrames  uint64
	LastRSSI       int8
}

func NewAggregator(bindAddr string, port int, bufSize int) *Aggregator {
	return &Aggregator{
		bindAddr:  bindAddr,
		port:      port,
		frames:    make(chan *csi.CsiFrame, bufSize),
		vitals:    make(chan *EdgeVitals, bufSize),
		nodeStats: make(map[uint8]*NodeStats),
	}
}

func (a *Aggregator) Frames() <-chan *csi.CsiFrame { return a.frames }
func (a *Aggregator) Vitals() <-chan *EdgeVitals    { return a.vitals }

func (a *Aggregator) Start() error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", a.bindAddr, a.port))
	if err != nil {
		return fmt.Errorf("resolve UDP addr: %w", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen UDP: %w", err)
	}
	a.conn = conn
	a.running = true

	log.Printf("[aggregator] listening on %s:%d", a.bindAddr, a.port)
	go a.receiveLoop()
	return nil
}

func (a *Aggregator) Stop() {
	a.mu.Lock()
	a.running = false
	a.mu.Unlock()
	if a.conn != nil {
		a.conn.Close()
	}
}

func (a *Aggregator) Stats() map[uint8]*NodeStats {
	a.statsMu.RLock()
	defer a.statsMu.RUnlock()
	result := make(map[uint8]*NodeStats, len(a.nodeStats))
	for k, v := range a.nodeStats {
		cp := *v
		result[k] = &cp
	}
	return result
}

func (a *Aggregator) receiveLoop() {
	buf := make([]byte, 4096)
	for a.running {
		n, _, err := a.conn.ReadFromUDP(buf)
		if err != nil {
			if a.running {
				log.Printf("[aggregator] read error: %v", err)
			}
			continue
		}
		if n < 4 {
			continue
		}
		a.dispatch(buf[:n])
	}
}

func (a *Aggregator) dispatch(data []byte) {
	if len(data) < 4 {
		return
	}
	magic := uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24

	switch magic {
	case MagicCSI:
		frame, err := ParseCSIFrame(data)
		if err != nil {
			log.Printf("[aggregator] parse CSI error: %v", err)
			return
		}
		a.trackNode(frame)
		select {
		case a.frames <- frame:
		default:
			log.Printf("[aggregator] frame channel full, dropping frame from node %d", frame.Metadata.NodeID)
		}

	case MagicVitals:
		v, err := ParseEdgeVitals(data)
		if err != nil {
			log.Printf("[aggregator] parse vitals error: %v", err)
			return
		}
		select {
		case a.vitals <- v:
		default:
		}
	}
}

func (a *Aggregator) trackNode(frame *csi.CsiFrame) {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()

	nodeID := frame.Metadata.NodeID
	stats, ok := a.nodeStats[nodeID]
	if !ok {
		stats = &NodeStats{}
		a.nodeStats[nodeID] = stats
		log.Printf("[aggregator] new node discovered: %d", nodeID)
	}

	if stats.FramesReceived > 0 {
		expected := stats.LastSequence + 1
		if frame.Metadata.Sequence > expected {
			stats.DroppedFrames += uint64(frame.Metadata.Sequence - expected)
		}
	}

	stats.FramesReceived++
	stats.LastSequence = frame.Metadata.Sequence
	stats.LastRSSI = frame.Metadata.RssiDBm
}
