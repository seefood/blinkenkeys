package hid

import (
	"fmt"

	goHid "github.com/sstallion/go-hid"
)

// Info is one attached HID interface as reported by enumeration — the
// subset of go-hid's DeviceInfo this package needs, kept as our own type so
// filterVial can be unit-tested without go-hid's cgo-backed Enumerate.
type Info struct {
	Path      string
	VendorID  uint16
	ProductID uint16
	UsagePage uint16
	Usage     uint16
}

func enumerateRaw() ([]Info, error) {
	var out []Info
	err := goHid.Enumerate(goHid.VendorIDAny, goHid.ProductIDAny, func(info *goHid.DeviceInfo) error {
		out = append(out, Info{
			Path:      info.Path,
			VendorID:  info.VendorID,
			ProductID: info.ProductID,
			UsagePage: info.UsagePage,
			Usage:     info.Usage,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hid: enumerate: %w", err)
	}
	return out, nil
}

// filterVial keeps only the raw-HID vendor interface (usage page 0xFF60,
// usage 0x61) that Vial firmware exposes for VIA/Vial/VialRGB commands —
// not a device's other HID interfaces (keyboard, mouse, etc).
func filterVial(infos []Info) []Info {
	var out []Info
	for _, info := range infos {
		if info.UsagePage == UsagePageVial && info.Usage == UsageVial {
			out = append(out, info)
		}
	}
	return out
}

// Enumerate lists every currently attached Vial-capable raw-HID interface,
// vendor/product-ID agnostic (this must generalize to any Vial-capable
// device, not one hardcoded board). Callers must have already called
// goHid.Init() at process startup (and goHid.Exit() at shutdown).
func Enumerate() ([]Info, error) {
	infos, err := enumerateRaw()
	if err != nil {
		return nil, err
	}
	return filterVial(infos), nil
}

// Open opens the Vial-capable raw-HID interface at path (as returned by
// Enumerate) for VIA/Vial/VialRGB commands.
func Open(path string) (*Device, error) {
	raw, err := goHid.OpenPath(path)
	if err != nil {
		return nil, fmt.Errorf("hid: open %s: %w", path, err)
	}
	return newDevice(raw), nil
}

// Controller is the subset of Device's behavior internal/dispatcher depends
// on, so tests can substitute a fake without opening real hardware.
type Controller interface {
	SetKeys(keys []KeyColor) error
	GetNumberLEDs() (uint16, error)
	GetLEDInfo(index uint16) (row, col uint8, err error)
	Close() error
}

// Identity is a resolved identity for one attached device, gathered by
// blinkenkeysd's startup enumeration (and re-gathered on each hotplug poll —
// see internal/dispatcher's Registry) via the enumeration path plus a
// GetKeyboardUID probe.
type Identity struct {
	Path      string
	VendorID  uint16
	ProductID uint16
	UID       [8]byte
	HasUID    bool
}

// BaseName computes d's pre-dedup identity key: Vial UID if available, else
// VID/PID + platform path, else VID/PID alone. Exported so
// internal/dispatcher's Registry (Task 6) can use it directly as the sole
// naming/identity-matching primitive for its stateful Reconcile — dedup
// suffixing (-0, -1, ...) and reconnect/"untethered" rewire matching both
// live in Registry now, since both need state that persists across polls
// (which name is already taken; which slot is untethered) rather than a
// stateless one-shot pass over a single enumeration snapshot. Two Identity
// values are considered the same device iff BaseName(a) == BaseName(b).
func BaseName(d Identity) string {
	switch {
	case d.HasUID:
		return fmt.Sprintf("uid-%x", d.UID)
	case d.Path != "":
		return fmt.Sprintf("%04x-%04x-%s", d.VendorID, d.ProductID, d.Path)
	default:
		return fmt.Sprintf("%04x-%04x", d.VendorID, d.ProductID)
	}
}
