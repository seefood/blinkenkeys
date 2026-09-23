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

// Device is an open connection to one Vial-capable raw-HID interface.
type Device struct {
	raw rawDevice
}

func newDevice(raw rawDevice) *Device { return &Device{raw: raw} }

// Close releases the underlying HID handle.
func (d *Device) Close() error { return d.raw.Close() }

// GetKeyboardUID returns the firmware's compile-time VIAL_KEYBOARD_UID, the
// most stable device-identity signal available (survives USB port changes).
// Per quantum/vial.c's vial_get_keyboard_id handler, the reply layout is:
// bytes 0-3 protocol version (unused here), bytes 4-11 the 8-byte UID.
func (d *Device) GetKeyboardUID() ([8]byte, error) {
	var uid [8]byte
	resp, err := sendReport(d.raw, []byte{idVialPrefix, vialGetKeyboardID})
	if err != nil {
		return uid, err
	}
	copy(uid[:], resp[4:12])
	return uid, nil
}

// GetNumberLEDs returns the device's total LED count, via VialRGB's
// VIALRGB_GET_NUMBER_LEDS.
func (d *Device) GetNumberLEDs() (uint16, error) {
	resp, err := sendReport(d.raw, []byte{cmdViaLightingGetValue, valVialRGBGetNumberLEDs})
	if err != nil {
		return 0, err
	}
	return uint16(resp[2]) | uint16(resp[3])<<8, nil
}

// GetLEDInfo returns the matrix row/col position of the given LED index, via
// VialRGB's VIALRGB_GET_LED_INFO.
func (d *Device) GetLEDInfo(index uint16) (row, col uint8, err error) {
	resp, err := sendReport(d.raw, []byte{
		cmdViaLightingGetValue, valVialRGBGetLEDInfo,
		byte(index), byte(index >> 8),
	})
	if err != nil {
		return 0, 0, err
	}
	return resp[5], resp[6], nil
}

// SetDirectMode switches the device into VialRGB's Direct mode (host-
// controlled, RAM-only colors), required before SetKeys has any effect.
func (d *Device) SetDirectMode() error {
	_, err := sendReport(d.raw, []byte{
		cmdViaLightingSetValue, valVialRGBSetMode, effectDirect, 0x00,
	})
	return err
}

// KeyColor is one LED's target color, in QMK-native HSV (each 0-255).
type KeyColor struct {
	Index   uint16
	H, S, V uint8
}

// SetKeys sets up to maxKeysPerReport (9) LEDs in one HID report, via
// VialRGB's VIALRGB_DIRECT_FASTSET. keys must have contiguous, ascending
// Index values — the wire format encodes only a start index and a count.
// internal/dispatcher is responsible for chunking accordingly before
// calling this.
func (d *Device) SetKeys(keys []KeyColor) error {
	if len(keys) == 0 {
		return nil
	}
	if len(keys) > maxKeysPerReport {
		return fmt.Errorf("hid: SetKeys got %d keys, max %d per report", len(keys), maxKeysPerReport)
	}
	start := keys[0].Index
	payload := []byte{
		cmdViaLightingSetValue, valVialRGBDirectFastSet,
		byte(start), byte(start >> 8), byte(len(keys)),
	}
	for i, k := range keys {
		if k.Index != start+uint16(i) {
			return fmt.Errorf("hid: SetKeys: index %d not contiguous from %d", k.Index, start)
		}
		payload = append(payload, k.H, k.S, k.V)
	}
	_, err := sendReport(d.raw, payload)
	return err
}
