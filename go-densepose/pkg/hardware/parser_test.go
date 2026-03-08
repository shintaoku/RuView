package hardware

import (
	"encoding/binary"
	"math"
	"testing"
)

func buildCSIPacket(nodeID uint8, nAnt uint8, nSub uint16, freqMHz uint32, seq uint32, rssi int8, noise int8, iqPairs [][2]int8) []byte {
	buf := make([]byte, HeaderSize+len(iqPairs)*2)
	binary.LittleEndian.PutUint32(buf[0:4], MagicCSI)
	buf[4] = nodeID
	buf[5] = nAnt
	binary.LittleEndian.PutUint16(buf[6:8], nSub)
	binary.LittleEndian.PutUint32(buf[8:12], freqMHz)
	binary.LittleEndian.PutUint32(buf[12:16], seq)
	buf[16] = byte(rssi)
	buf[17] = byte(noise)
	for i, pair := range iqPairs {
		buf[HeaderSize+i*2] = byte(pair[0])
		buf[HeaderSize+i*2+1] = byte(pair[1])
	}
	return buf
}

func TestParseCSIFrame_Valid(t *testing.T) {
	iq := make([][2]int8, 3*2) // 3 antennas × 2 subcarriers
	iq[0] = [2]int8{3, 4}     // amp=5
	iq[1] = [2]int8{5, 12}    // amp=13
	iq[2] = [2]int8{1, 0}
	iq[3] = [2]int8{0, 1}
	iq[4] = [2]int8{-3, -4}
	iq[5] = [2]int8{10, 0}

	pkt := buildCSIPacket(1, 3, 2, 2412, 42, -45, -95, iq)
	frame, err := ParseCSIFrame(pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if frame.Metadata.NodeID != 1 {
		t.Errorf("NodeID = %d, want 1", frame.Metadata.NodeID)
	}
	if frame.Metadata.Antennas.RxAntennas != 3 {
		t.Errorf("RxAntennas = %d, want 3", frame.Metadata.Antennas.RxAntennas)
	}
	if frame.Metadata.NSubcarriers != 2 {
		t.Errorf("NSubcarriers = %d, want 2", frame.Metadata.NSubcarriers)
	}
	if frame.Metadata.ChannelFreqMHz != 2412 {
		t.Errorf("FreqMHz = %d, want 2412", frame.Metadata.ChannelFreqMHz)
	}
	if frame.Metadata.Sequence != 42 {
		t.Errorf("Sequence = %d, want 42", frame.Metadata.Sequence)
	}
	if frame.Metadata.RssiDBm != -45 {
		t.Errorf("RSSI = %d, want -45", frame.Metadata.RssiDBm)
	}

	// 6 subcarriers total (3 ant × 2 sc)
	if len(frame.Amplitude) != 6 {
		t.Errorf("len(Amplitude) = %d, want 6", len(frame.Amplitude))
	}
	// First subcarrier: I=3, Q=4 → amp=5
	if math.Abs(frame.Amplitude[0]-5.0) > 0.001 {
		t.Errorf("Amplitude[0] = %v, want 5.0", frame.Amplitude[0])
	}
}

func TestParseCSIFrame_InvalidMagic(t *testing.T) {
	pkt := make([]byte, HeaderSize)
	binary.LittleEndian.PutUint32(pkt[0:4], 0xDEADBEEF)
	_, err := ParseCSIFrame(pkt)
	if err == nil {
		t.Error("expected error for invalid magic")
	}
}

func TestParseCSIFrame_TooShort(t *testing.T) {
	_, err := ParseCSIFrame([]byte{1, 2, 3})
	if err == nil {
		t.Error("expected error for short packet")
	}
}

func TestParseCSIFrame_InsufficientIQ(t *testing.T) {
	pkt := buildCSIPacket(1, 1, 56, 2412, 0, -50, -90, nil)
	pkt = pkt[:HeaderSize] // strip IQ data
	_, err := ParseCSIFrame(pkt)
	if err == nil {
		t.Error("expected error for missing IQ data")
	}
}

func TestParseCSIFrame_5GHz(t *testing.T) {
	iq := make([][2]int8, 1)
	iq[0] = [2]int8{1, 0}
	pkt := buildCSIPacket(2, 1, 1, 5180, 1, -60, -90, iq)

	frame, err := ParseCSIFrame(pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if frame.Metadata.FreqBand.String() != "5GHz" {
		t.Errorf("FreqBand = %s, want 5GHz", frame.Metadata.FreqBand)
	}
}

func TestParseEdgeVitals_Valid(t *testing.T) {
	pkt := make([]byte, VitalsPacketSize)
	binary.LittleEndian.PutUint32(pkt[0:4], MagicVitals)
	pkt[4] = 1                                    // node_id
	pkt[5] = 0x05                                 // presence + motion
	binary.LittleEndian.PutUint16(pkt[6:8], 1500) // 15.0 BPM
	binary.LittleEndian.PutUint32(pkt[8:12], 720000) // 72.0 BPM
	pkt[12] = 0xC9 // -55 as unsigned byte
	pkt[13] = 2   // n_persons

	v, err := ParseEdgeVitals(pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.NodeID != 1 {
		t.Errorf("NodeID = %d, want 1", v.NodeID)
	}
	if !v.Presence {
		t.Error("expected presence")
	}
	if !v.Motion {
		t.Error("expected motion")
	}
	if v.FallDetected {
		t.Error("did not expect fall")
	}
	// breathRaw = 1500, / 100.0 = 15.0
	if math.Abs(v.BreathingBPM-15.0) > 0.01 {
		t.Errorf("BreathingBPM = %v, want 15.0", v.BreathingBPM)
	}
	// hrRaw = 720000, / 10000.0 = 72.0
	if math.Abs(v.HeartRateBPM-72.0) > 0.01 {
		t.Errorf("HeartRateBPM = %v, want 72.0", v.HeartRateBPM)
	}
}
