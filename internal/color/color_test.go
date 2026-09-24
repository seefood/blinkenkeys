package color

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		h, s, v uint8
		wantErr bool
	}{
		{"hex red", "#ff0000", 0, 255, 255, false},
		{"hex green (css dark green)", "#008000", 85, 255, 128, false},
		{"hex white", "#ffffff", 0, 0, 255, false},
		{"hex black", "#000000", 0, 0, 0, false},
		{"hsv triple", "0,128,255", 0, 128, 255, false},
		{"hsv triple spaced", "10, 20, 30", 10, 20, 30, false},
		{"named green", "green", 85, 255, 128, false},
		{"named case-insensitive", "GREEN", 85, 255, 128, false},
		{"unknown name", "not-a-color", 0, 0, 0, true},
		{"malformed hex", "#zzzzzz", 0, 0, 0, true},
		{"short hex", "#fff", 0, 0, 0, true},
		{"triple value out of range", "256,0,0", 0, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, s, v, err := Parse(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = nil error, want error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.in, err)
			}
			if h != tt.h || s != tt.s || v != tt.v {
				t.Errorf("Parse(%q) = %d,%d,%d; want %d,%d,%d", tt.in, h, s, v, tt.h, tt.s, tt.v)
			}
		})
	}
}

func TestParseHSV(t *testing.T) {
	got, err := ParseHSV("#ff0000")
	if err != nil {
		t.Fatalf("ParseHSV: %v", err)
	}
	if got != (HSV{H: 0, S: 255, V: 255}) {
		t.Errorf("ParseHSV(#ff0000) = %+v", got)
	}
	if _, err := ParseHSV("not-a-color"); err == nil {
		t.Error("ParseHSV(not-a-color): want error")
	}
}
