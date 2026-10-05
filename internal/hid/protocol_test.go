package hid

import (
	"bytes"
	"errors"
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
	readErrs []error // consumed one per ReadWithTimeout call, before replies
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
	if len(f.readErrs) > 0 {
		err := f.readErrs[0]
		f.readErrs = f.readErrs[1:]
		return 0, err
	}
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
	reply[0], reply[1] = cmdViaLightingGetValue, valVialRGBGetNumberLEDs // echoed request
	reply[2], reply[3] = 0x2C, 0x01                                      // 300 little-endian
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
	reply[0], reply[1] = cmdViaLightingGetValue, valVialRGBGetLEDInfo // echoed request
	reply[5], reply[6] = 2, 3                                         // row, col
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

// hidapi's Linux backend reports an interrupted poll() as a plain error
// string (strerror), not a typed errno; Go's runtime signals threads often.
var errInterrupted = errors.New("Interrupted system call") //nolint:staticcheck // verbatim glibc strerror(EINTR)

func TestSendReportRetriesInterruptedReadWithoutRewriting(t *testing.T) {
	reply := make([]byte, ReportLen)
	dev := &fakeDevice{readErrs: []error{errInterrupted, errInterrupted}, replies: [][]byte{reply}}
	if _, err := sendReport(dev, []byte{0x01}); err != nil {
		t.Fatalf("sendReport: %v", err)
	}
	if len(dev.writes) != 1 {
		t.Errorf("wrote %d times, want 1: the reply to the first write is still pending", len(dev.writes))
	}
}

func TestSendReportGivesUpOnPersistentInterrupts(t *testing.T) {
	dev := &fakeDevice{readErrs: []error{errInterrupted, errInterrupted, errInterrupted, errInterrupted, errInterrupted}}
	if _, err := sendReport(dev, []byte{0x01}); err == nil {
		t.Fatal("want error after bounded retries")
	}
}

func TestSendReportDoesNotRetryOtherReadErrors(t *testing.T) {
	dev := &fakeDevice{readErrs: []error{fmt.Errorf("device disconnected")}, replies: [][]byte{make([]byte, ReportLen)}}
	if _, err := sendReport(dev, []byte{0x01}); err == nil {
		t.Fatal("want the read error returned")
	}
}

func TestGetNumberLEDsRejectsReplyToAnotherRequest(t *testing.T) {
	// A stale Vial keyboard-ID reply (06 00 00 00 <uid>) would otherwise parse as 0 LEDs.
	stale := make([]byte, ReportLen)
	stale[0] = 0x06
	dev := &fakeDevice{replies: [][]byte{stale}}
	if n, err := newDevice(dev).GetNumberLEDs(); err == nil {
		t.Errorf("GetNumberLEDs = %d, nil; want an error for a reply that does not echo 08 43", n)
	}
}

func TestGetLEDInfoRejectsReplyToAnotherRequest(t *testing.T) {
	stale := make([]byte, ReportLen)
	stale[0] = 0x06
	dev := &fakeDevice{replies: [][]byte{stale}}
	if _, _, err := newDevice(dev).GetLEDInfo(0); err == nil {
		t.Error("GetLEDInfo: want an error for a reply that does not echo 08 44")
	}
}
