package dispatcher

import (
	"testing"

	"github.com/seefood/blinkenkeys/internal/hid"
)

func setKeyJob(device string, index uint16) job {
	return job{kind: opSetKey, device: device, key: hid.KeyColor{Index: index}, reply: make(chan jobResult, 1)}
}

func TestGroupForSendBatchesContiguous(t *testing.T) {
	batch := []job{
		setKeyJob("a", 0),
		setKeyJob("a", 1),
		setKeyJob("a", 2),  // contiguous, same device -> one group
		setKeyJob("b", 5),  // different device -> its own group
		setKeyJob("a", 10), // non-contiguous -> its own group
	}
	groups := groupForSend(batch)
	if len(groups) != 3 {
		t.Fatalf("got %d groups, want 3", len(groups))
	}
	if len(groups[0]) != 3 {
		t.Errorf("group 0 has %d items, want 3", len(groups[0]))
	}
	if len(groups[1]) != 1 || len(groups[2]) != 1 {
		t.Errorf("groups 1,2 want size 1 each, got %d,%d", len(groups[1]), len(groups[2]))
	}
}

func TestGroupForSendCapsAtNine(t *testing.T) {
	batch := make([]job, 12)
	for i := range batch {
		batch[i] = setKeyJob("a", uint16(i))
	}
	groups := groupForSend(batch)
	if len(groups) != 2 || len(groups[0]) != 9 || len(groups[1]) != 3 {
		t.Fatalf("got group sizes %v; want [9 3]", groupSizes(groups))
	}
}

func TestGroupForSendNonSetKeyAlwaysSingleton(t *testing.T) {
	batch := []job{
		{kind: opListDevices, reply: make(chan jobResult, 1)},
		{kind: opListDevices, reply: make(chan jobResult, 1)},
	}
	groups := groupForSend(batch)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2 (non-SetKey ops never merge)", len(groups))
	}
}

func groupSizes(groups [][]job) []int {
	sizes := make([]int, len(groups))
	for i, g := range groups {
		sizes[i] = len(g)
	}
	return sizes
}
