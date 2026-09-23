// Package hid implements the raw-HID VIA/Vial/VialRGB protocol used to
// enumerate Vial-capable keyboards and set their LED colors, verified
// against quantum/via.h, quantum/vial.h, quantum/vial.c, and quantum/vialrgb.c
// in vial-qmk, and against the working reference in set_key_color.py.
package hid

import (
	"fmt"
	"time"
)

const (
	// ReportLen is the fixed HID report size this device uses.
	ReportLen = 32

	// UsagePageVial and UsageVial identify the Vial raw-HID vendor
	// interface among a device's other HID interfaces (keyboard, mouse).
	UsagePageVial = 0xFF60
	UsageVial     = 0x61

	cmdViaLightingSetValue = 0x07
	cmdViaLightingGetValue = 0x08

	valVialRGBSetMode       = 0x41
	valVialRGBDirectFastSet = 0x42
	valVialRGBGetNumberLEDs = 0x43
	valVialRGBGetLEDInfo    = 0x44

	effectDirect = 1

	idVialPrefix      = 0xFE
	vialGetKeyboardID = 0x00

	// maxKeysPerReport is VialRGB's own per-packet ceiling: 27 usable
	// payload bytes ÷ 3 bytes/LED (vialrgb.c's fast_set_leds comment).
	maxKeysPerReport = 9

	reportTimeout = 1 * time.Second
)

// rawDevice is the subset of *goHid.Device this package depends on, so
// tests can substitute a fake without opening real hardware.
type rawDevice interface {
	Write(p []byte) (int, error)
	ReadWithTimeout(p []byte, timeout time.Duration) (int, error)
	Close() error
}

// sendReport writes a 32-byte HID report (payload, zero-padded) prefixed
// with the mandatory report-ID byte (0x00 — this device has no numbered
// reports), then reads the 32-byte reply. This mirrors set_key_color.py's
// already hardware-validated send() exactly.
func sendReport(dev rawDevice, payload []byte) ([]byte, error) {
	if len(payload) > ReportLen {
		return nil, fmt.Errorf("hid: payload of %d bytes exceeds %d-byte report", len(payload), ReportLen)
	}
	buf := make([]byte, 1+ReportLen)
	copy(buf[1:], payload)

	if _, err := dev.Write(buf); err != nil {
		return nil, fmt.Errorf("hid: write: %w", err)
	}
	resp := make([]byte, ReportLen)
	n, err := dev.ReadWithTimeout(resp, reportTimeout)
	if err != nil {
		return nil, fmt.Errorf("hid: read: %w", err)
	}
	if n != ReportLen {
		return nil, fmt.Errorf("hid: short read: got %d bytes, want %d", n, ReportLen)
	}
	return resp, nil
}
