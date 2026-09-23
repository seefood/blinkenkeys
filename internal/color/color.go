// Package color parses the REST API's color field into QMK-native HSV
// bytes, per the design spec's three accepted forms.
package color

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/image/colornames"
)

// Parse converts a color string into QMK-native HSV: each of H, S, V is
// 0-255, not the more common 0-360°/0-100%/0-100% convention. Accepted
// forms, in this detection order: "#rrggbb" hex; an "H,S,V" triple (each
// 0-255, passed through unchanged — it's already QMK-native); a CSS/X11
// color name (e.g. "green"). Anything else is an error.
func Parse(s string) (h, sat, v uint8, err error) {
	if strings.HasPrefix(s, "#") {
		return parseHex(s)
	}
	if h, sat, v, ok := tryParseTriple(s); ok {
		return h, sat, v, nil
	}
	if h, sat, v, ok := tryParseName(s); ok {
		return h, sat, v, nil
	}
	return 0, 0, 0, fmt.Errorf("color: cannot parse %q", s)
}

func parseHex(s string) (h, sat, v uint8, err error) {
	if len(s) != 7 {
		return 0, 0, 0, fmt.Errorf("color: invalid hex %q: want #rrggbb", s)
	}
	r, err1 := strconv.ParseUint(s[1:3], 16, 8)
	g, err2 := strconv.ParseUint(s[3:5], 16, 8)
	b, err3 := strconv.ParseUint(s[5:7], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, fmt.Errorf("color: invalid hex %q", s)
	}
	h, sat, v = rgbToHSV(uint8(r), uint8(g), uint8(b))
	return h, sat, v, nil
}

func tryParseTriple(s string) (h, sat, v uint8, ok bool) {
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	vals := make([]uint8, 3)
	for i, p := range parts {
		n, err := strconv.ParseUint(strings.TrimSpace(p), 10, 8)
		if err != nil {
			return 0, 0, 0, false
		}
		vals[i] = uint8(n)
	}
	return vals[0], vals[1], vals[2], true
}

func tryParseName(s string) (h, sat, v uint8, ok bool) {
	c, found := colornames.Map[strings.ToLower(s)]
	if !found {
		return 0, 0, 0, false
	}
	h, sat, v = rgbToHSV(c.R, c.G, c.B)
	return h, sat, v, true
}

// rgbToHSV converts 8-bit RGB to QMK-native HSV: H, S, and V all scaled to
// 0-255 (not 360°/100%/100%), matching quantum/color.h's HSV type.
func rgbToHSV(r, g, b uint8) (h, s, v uint8) {
	max := maxu8(r, g, b)
	min := minu8(r, g, b)
	delta := int(max) - int(min)

	v = max
	if max == 0 {
		return 0, 0, 0
	}
	s = uint8(delta * 255 / int(max)) // #nosec G115 -- delta <= max (delta = max-min, min >= 0), so delta*255/max <= 255
	if delta == 0 {
		return 0, s, v
	}

	var hDeg float64
	switch max {
	case r:
		hDeg = 60 * (float64(int(g)-int(b)) / float64(delta))
		if hDeg < 0 {
			hDeg += 360
		}
	case g:
		hDeg = 60*(float64(int(b)-int(r))/float64(delta)) + 120
	default: // b
		hDeg = 60*(float64(int(r)-int(g))/float64(delta)) + 240
	}
	h = uint8(hDeg/360*255 + 0.5)
	return h, s, v
}

func maxu8(a, b, c uint8) uint8 {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}

func minu8(a, b, c uint8) uint8 {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
