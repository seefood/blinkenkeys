// Command setkeycolor is a minimal hardware smoke test for internal/hid: it
// opens the first attached Vial-capable device and sets one LED's color,
// with no dispatcher, HTTP server, or config — those belong to the real
// cmd/blinkenkeysd, not yet built. See docs/superpowers/plans for the full
// Phase 1+2 plan this is a deliberately reduced first slice of.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"

	goHid "github.com/sstallion/go-hid"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/hid"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "setkeycolor:", err)
		os.Exit(1)
	}
}

func run() error {
	index := flag.Uint("index", 0, "LED index to set")
	colorStr := flag.String("color", "red", "color: #rrggbb, an H,S,V triple (0-255 each), or a CSS/X11 name")
	flag.Parse()

	h, s, v, err := color.Parse(*colorStr)
	if err != nil {
		return fmt.Errorf("parsing -color: %w", err)
	}
	if *index > math.MaxUint16 {
		return fmt.Errorf("-index %d exceeds max LED index %d", *index, math.MaxUint16)
	}

	if err := goHid.Init(); err != nil {
		return fmt.Errorf("hid init: %w", err)
	}
	defer goHid.Exit()

	devices, err := hid.Enumerate()
	if err != nil {
		return fmt.Errorf("enumerate: %w", err)
	}
	if len(devices) == 0 {
		return fmt.Errorf("no Vial-capable devices found")
	}

	dev, err := hid.Open(devices[0].Path)
	if err != nil {
		return fmt.Errorf("open %s: %w", devices[0].Path, err)
	}
	defer dev.Close()

	if err := dev.SetDirectMode(); err != nil {
		return fmt.Errorf("set direct mode: %w", err)
	}
	if err := dev.SetKeys([]hid.KeyColor{{Index: uint16(*index), H: h, S: s, V: v}}); err != nil {
		return fmt.Errorf("set key %d: %w", *index, err)
	}

	fmt.Printf("set key %d on %s to h=%d s=%d v=%d\n", *index, devices[0].Path, h, s, v)
	return nil
}
