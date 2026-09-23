package hid

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

// fakeDevice substitutes for *goHid.Device in tests, recording writes and
// replaying queued responses — no real hardware needed.
type fakeDevice struct {
	writes   [][]byte
	replies  [][]byte
	i        int
	writeErr error
	readErr  error
}

func (f *fakeDevice) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	cp := make([]byte, len(p))
	copy(cp, p)
	f.writes = append(f.writes, cp)
	return len(p), nil
}

func (f *fakeDevice) ReadWithTimeout(p []byte, _ time.Duration) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	if f.i >= len(f.replies) {
		return 0, fmt.Errorf("fakeDevice: no more replies queued")
	}
	reply := f.replies[f.i]
	f.i++
	return copy(p, reply), nil
}

func (f *fakeDevice) Close() error { return nil }

func TestSendReport(t *testing.T) {
	reply := make([]byte, ReportLen)
	reply[0] = 0xAB
	dev := &fakeDevice{replies: [][]byte{reply}}

	got, err := sendReport(dev, []byte{cmdViaLightingGetValue, valVialRGBGetNumberLEDs})
	if err != nil {
		t.Fatalf("sendReport: %v", err)
	}
	if !bytes.Equal(got, reply) {
		t.Errorf("sendReport reply = %x, want %x", got, reply)
	}
	if len(dev.writes) != 1 {
		t.Fatalf("got %d writes, want 1", len(dev.writes))
	}
	wantWrite := make([]byte, 1+ReportLen)
	wantWrite[1] = cmdViaLightingGetValue
	wantWrite[2] = valVialRGBGetNumberLEDs
	if !bytes.Equal(dev.writes[0], wantWrite) {
		t.Errorf("wrote %x, want %x", dev.writes[0], wantWrite)
	}
}

func TestSendReportShortRead(t *testing.T) {
	dev := &fakeDevice{replies: [][]byte{make([]byte, 10)}} // too short
	if _, err := sendReport(dev, []byte{0x01}); err == nil {
		t.Fatal("sendReport: want error on short read, got nil")
	}
}

func TestSendReportWriteError(t *testing.T) {
	dev := &fakeDevice{writeErr: fmt.Errorf("boom")}
	if _, err := sendReport(dev, []byte{0x01}); err == nil {
		t.Fatal("sendReport: want error when Write fails, got nil")
	}
}
