package dispatcher

// maxBatch is VialRGB's own per-packet ceiling (see internal/hid's
// maxKeysPerReport) — batches larger than this become multiple groups.
const maxBatch = 9

// groupForSend partitions a drained batch of jobs into groups that become
// one hid.Controller.SetKeys call each: non-SetKey jobs are always
// singleton groups; SetKey jobs are grouped per the design spec — same
// device, contiguous LED index, up to maxBatch — preserving arrival order.
func groupForSend(batch []job) [][]job {
	var out [][]job
	var run []job
	var runDevice string
	var runNextIndex uint16

	flush := func() {
		if len(run) > 0 {
			out = append(out, run)
			run = nil
		}
	}

	for _, j := range batch {
		if j.kind != opSetKey {
			flush()
			out = append(out, []job{j})
			continue
		}
		if len(run) > 0 && j.device == runDevice && j.key.Index == runNextIndex && len(run) < maxBatch {
			run = append(run, j)
			runNextIndex++
			continue
		}
		flush()
		run = []job{j}
		runDevice = j.device
		runNextIndex = j.key.Index + 1
	}
	flush()
	return out
}
