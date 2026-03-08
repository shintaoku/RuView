package hardware

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/ruvnet/go-densepose/pkg/csi"
)

const (
	MagicCSI       uint32 = 0xC511_0001
	MagicVitals    uint32 = 0xC511_0002
	MagicWASM      uint32 = 0xC511_0004
	HeaderSize            = 20
	VitalsPacketSize      = 32
)

type ParseError struct {
	Reason string
}

func (e *ParseError) Error() string { return "csi parse: " + e.Reason }

// ParseCSIFrame parses an ADR-018 binary CSI frame from raw UDP data.
//
// Wire format (little-endian):
//
//	[0:4]   magic    0xC5110001
//	[4]     node_id
//	[5]     n_antennas
//	[6:8]   n_subcarriers (u16)
//	[8:12]  freq_mhz (u32)
//	[12:16] sequence (u32)
//	[16]    rssi (i8)
//	[17]    noise (i8)
//	[18:20] reserved
//	[20..]  I/Q pairs: (i8, i8) × n_antennas × n_subcarriers
func ParseCSIFrame(data []byte) (*csi.CsiFrame, error) {
	if len(data) < HeaderSize {
		return nil, &ParseError{"insufficient data for header"}
	}

	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != MagicCSI {
		return nil, &ParseError{fmt.Sprintf("invalid magic: 0x%08X", magic)}
	}

	nodeID := data[4]
	nAnt := data[5]
	nSub := binary.LittleEndian.Uint16(data[6:8])
	freqMHz := binary.LittleEndian.Uint32(data[8:12])
	seq := binary.LittleEndian.Uint32(data[12:16])
	rssi := int8(data[16])
	noise := int8(data[17])

	if nAnt == 0 || nAnt > 8 {
		return nil, &ParseError{fmt.Sprintf("invalid antenna count: %d", nAnt)}
	}
	if nSub == 0 || nSub > csi.MaxSubcarriers {
		return nil, &ParseError{fmt.Sprintf("invalid subcarrier count: %d", nSub)}
	}

	iqCount := int(nAnt) * int(nSub)
	iqBytesNeeded := HeaderSize + iqCount*2
	if len(data) < iqBytesNeeded {
		return nil, &ParseError{fmt.Sprintf("need %d bytes for I/Q, got %d", iqBytesNeeded, len(data))}
	}

	subcarriers := make([]csi.SubcarrierData, iqCount)
	for i := 0; i < iqCount; i++ {
		offset := HeaderSize + i*2
		subcarriers[i] = csi.SubcarrierData{
			Index: i % int(nSub),
			I:     int16(int8(data[offset])),
			Q:     int16(int8(data[offset+1])),
		}
	}

	band := csi.Band2_4GHz
	if freqMHz >= 5000 {
		band = csi.Band5GHz
	} else if freqMHz >= 5925 {
		band = csi.Band6GHz
	}

	meta := csi.CsiMetadata{
		Timestamp:      time.Now(),
		DeviceID:       fmt.Sprintf("esp32-node%d", nodeID),
		NodeID:         nodeID,
		FreqBand:       band,
		ChannelFreqMHz: freqMHz,
		Antennas:       csi.AntennaConfig{TxAntennas: 1, RxAntennas: nAnt},
		RssiDBm:        rssi,
		NoiseFloorDBm:  noise,
		Sequence:       seq,
		NSubcarriers:   nSub,
	}

	return csi.NewCsiFrame(meta, subcarriers), nil
}

// EdgeVitals represents pre-computed vitals from ESP32 edge processing.
type EdgeVitals struct {
	NodeID         uint8
	Presence       bool
	FallDetected   bool
	Motion         bool
	BreathingBPM   float64
	HeartRateBPM   float64
	RssiDBm        int8
	NPersons       uint8
	MotionEnergy   float32
	PresenceScore  float32
	TimestampMs    uint32
}

// ParseEdgeVitals parses an ADR-018 edge vitals packet (magic 0xC5110002).
func ParseEdgeVitals(data []byte) (*EdgeVitals, error) {
	if len(data) < VitalsPacketSize {
		return nil, &ParseError{"insufficient data for vitals packet"}
	}

	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != MagicVitals {
		return nil, &ParseError{fmt.Sprintf("invalid vitals magic: 0x%08X", magic)}
	}

	flags := data[5]
	breathRaw := binary.LittleEndian.Uint16(data[6:8])
	hrRaw := binary.LittleEndian.Uint32(data[8:12])

	v := &EdgeVitals{
		NodeID:        data[4],
		Presence:      flags&0x01 != 0,
		FallDetected:  flags&0x02 != 0,
		Motion:        flags&0x04 != 0,
		BreathingBPM:  float64(breathRaw) / 100.0,
		HeartRateBPM:  float64(hrRaw) / 10000.0,
		RssiDBm:       int8(data[12]),
		NPersons:      data[13],
	}

	v.MotionEnergy = float32FromLE(data[16:20])
	v.PresenceScore = float32FromLE(data[20:24])
	v.TimestampMs = binary.LittleEndian.Uint32(data[24:28])

	return v, nil
}

func float32FromLE(b []byte) float32 {
	bits := binary.LittleEndian.Uint32(b)
	return float32FromBits(bits)
}

func float32FromBits(bits uint32) float32 {
	return math.Float32frombits(bits)
}
