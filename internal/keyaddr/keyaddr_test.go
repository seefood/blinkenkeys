package keyaddr

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    Address
		wantErr bool
	}{
		{"2,3", Address{Kind: RowCol, Row: 2, Col: 3}, false},
		{"led:7", Address{Kind: LED, N: 7}, false},
		{"idx:0", Address{Kind: Idx, N: 0}, false},
		{"esc", Address{Kind: Name, Name: "esc"}, false},
		{"", Address{}, true},
		{"led:", Address{}, true},
		{"led:x", Address{}, true},
		{"idx:-1", Address{}, true},
		{"led:65536", Address{}, true},
		{"1,2,3", Address{}, true},
		{"1,x", Address{}, true},
		{"256,0", Address{}, true},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in)
		if tt.wantErr {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Parse(%q) error = %v, want ErrInvalid", tt.in, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
	}
}

func TestStringRoundTrip(t *testing.T) {
	for _, in := range []string{"2,3", "led:7", "idx:4", "esc"} {
		a, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if a.String() != in {
			t.Errorf("Parse(%q).String() = %q", in, a.String())
		}
	}
}

func TestResolve(t *testing.T) {
	// Firmware order is not reading order (like the 12e4's serpentine), and
	// LED 3 has no matrix key (underglow): VialRGB reports it as 0xFF,0xFF.
	positions := []Position{
		{Index: 0, Row: 0, Col: 1},
		{Index: 1, Row: 0, Col: 0},
		{Index: 2, Row: 1, Col: 0},
		{Index: 3, Row: 0xFF, Col: 0xFF},
	}
	tests := []struct {
		addr   Address
		want   uint16
		wantOK bool
	}{
		{Address{Kind: RowCol, Row: 0, Col: 0}, 1, true},
		{Address{Kind: RowCol, Row: 1, Col: 0}, 2, true},
		{Address{Kind: RowCol, Row: 5, Col: 5}, 0, false},
		{Address{Kind: RowCol, Row: 0xFF, Col: 0xFF}, 0, false},
		{Address{Kind: LED, N: 3}, 3, true},
		{Address{Kind: LED, N: 4}, 0, false},
		{Address{Kind: Idx, N: 0}, 1, true}, // (0,0)
		{Address{Kind: Idx, N: 1}, 0, true}, // (0,1)
		{Address{Kind: Idx, N: 2}, 2, true}, // (1,0)
		{Address{Kind: Idx, N: 3}, 0, false},
		{Address{Kind: Name, Name: "esc"}, 0, false},
	}
	for _, tt := range tests {
		got, ok := Resolve(tt.addr, 4, positions)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("Resolve(%s) = %d, %v; want %d, %v", tt.addr, got, ok, tt.want, tt.wantOK)
		}
	}
}
