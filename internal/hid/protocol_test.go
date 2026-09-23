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

func TestGetKeyboardUID(t *testing.T) {
	reply := make([]byte, ReportLen)
	copy(reply[4:12], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	d := newDevice(&fakeDevice{replies: [][]byte{reply}})

	uid, err := d.GetKeyboardUID()
	if err != nil {
		t.Fatalf("GetKeyboardUID: %v", err)
	}
	want := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	if uid != want {
		t.Errorf("GetKeyboardUID = %v, want %v", uid, want)
	}
}

func TestGetNumberLEDs(t *testing.T) {
	reply := make([]byte, ReportLen)
	reply[2], reply[3] = 0x2C, 0x01 // 300 little-endian
	d := newDevice(&fakeDevice{replies: [][]byte{reply}})

	n, err := d.GetNumberLEDs()
	if err != nil {
		t.Fatalf("GetNumberLEDs: %v", err)
	}
	if n != 300 {
		t.Errorf("GetNumberLEDs = %d, want 300", n)
	}
}

func TestGetLEDInfo(t *testing.T) {
	reply := make([]byte, ReportLen)
	reply[5], reply[6] = 2, 3 // row, col
	d := newDevice(&fakeDevice{replies: [][]byte{reply}})

	row, col, err := d.GetLEDInfo(7)
	if err != nil {
		t.Fatalf("GetLEDInfo: %v", err)
	}
	if row != 2 || col != 3 {
		t.Errorf("GetLEDInfo = %d,%d, want 2,3", row, col)
	}
}

func TestSetDirectMode(t *testing.T) {
	fake := &fakeDevice{replies: [][]byte{make([]byte, ReportLen)}}
	d := newDevice(fake)
	if err := d.SetDirectMode(); err != nil {
		t.Fatalf("SetDirectMode: %v", err)
	}
	want := make([]byte, 1+ReportLen)
	want[1] = cmdViaLightingSetValue
	want[2] = valVialRGBSetMode
	want[3] = effectDirect
	if !bytes.Equal(fake.writes[0], want) {
		t.Errorf("wrote %x, want %x", fake.writes[0], want)
	}
}

func TestSetKeys(t *testing.T) {
	fake := &fakeDevice{replies: [][]byte{make([]byte, ReportLen)}}
	d := newDevice(fake)

	if err := d.SetKeys([]KeyColor{{Index: 5, H: 0, S: 255, V: 255}}); err != nil {
		t.Fatalf("SetKeys: %v", err)
	}
	want := make([]byte, 1+ReportLen)
	want[1] = cmdViaLightingSetValue
	want[2] = valVialRGBDirectFastSet
	want[3] = 5 // start index low
	want[4] = 0 // start index high
	want[5] = 1 // count
	want[6], want[7], want[8] = 0, 255, 255
	if !bytes.Equal(fake.writes[0], want) {
		t.Errorf("wrote %x, want %x", fake.writes[0], want)
	}
}

func TestSetKeysNonContiguous(t *testing.T) {
	d := newDevice(&fakeDevice{})
	err := d.SetKeys([]KeyColor{{Index: 5}, {Index: 7}})
	if err == nil {
		t.Fatal("SetKeys: want error for non-contiguous indices")
	}
}

func TestSetKeysTooMany(t *testing.T) {
	d := newDevice(&fakeDevice{})
	keys := make([]KeyColor, maxKeysPerReport+1)
	for i := range keys {
		keys[i].Index = uint16(i)
	}
	if err := d.SetKeys(keys); err == nil {
		t.Fatal("SetKeys: want error for more than maxKeysPerReport keys")
	}
}
