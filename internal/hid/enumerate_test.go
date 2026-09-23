package hid

import "testing"

func TestFilterVial(t *testing.T) {
	infos := []Info{
		{Path: "/dev/hidraw0", UsagePage: 0x0001, Usage: 0x0006}, // a keyboard interface, not ours
		{Path: "/dev/hidraw1", UsagePage: UsagePageVial, Usage: UsageVial},
	}
	got := filterVial(infos)
	if len(got) != 1 || got[0].Path != "/dev/hidraw1" {
		t.Errorf("filterVial = %+v, want only /dev/hidraw1", got)
	}
}
