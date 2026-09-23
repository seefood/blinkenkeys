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
