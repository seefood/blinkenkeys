package dispatcher

// maxBatch is VialRGB's own per-packet ceiling (see internal/hid's
// maxKeysPerReport) — batches larger than this become multiple groups.
const maxBatch = 9

// groupForSend partitions a drained batch of jobs into groups that become
// one hid.Controller.SetKeys call each: non-flush jobs are always singleton
// groups; flush jobs are grouped per the design spec — same device,
// contiguous LED index, up to maxBatch — preserving arrival order.
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
		if j.kind != opFlush {
			flush()
			out = append(out, []job{j})
			continue
		}
		if len(run) > 0 && j.device == runDevice && j.index == runNextIndex && len(run) < maxBatch {
			run = append(run, j)
			runNextIndex++
			continue
		}
		flush()
		run = []job{j}
		runDevice = j.device
		runNextIndex = j.index + 1
	}
	flush()
	return out
}

// dedupeFlushes drops repeat flushes of the same (device, LED) within one
// drained batch, keeping the first occurrence's position. Keeping any one
// is correct because flushes read the cache at dispatch time.
func dedupeFlushes(batch []job) []job {
	type key struct {
		device string
		index  uint16
	}
	seen := make(map[key]bool)
	out := make([]job, 0, len(batch))
	for _, j := range batch {
		if j.kind == opFlush {
			k := key{j.device, j.index}
			if seen[k] {
				continue
			}
			seen[k] = true
		}
		out = append(out, j)
	}
	return out
}
