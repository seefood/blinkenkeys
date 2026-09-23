# Phase 1+2 hardware round-trip check

Manual, gated on physical hardware — not run in CI. Run this after any
change touching `internal/hid`, `internal/dispatcher`, or `cmd/blinkenkeysd`,
before considering that change verified end-to-end.

Prerequisites: `cxt_studio/12e4` attached, `personal/vialrgb-direct/001-enable`
firmware flashed (see `../../../README.md` and the parent `CXT-studio`
tree's `../qmk_vial`).

1. `make build`
2. Run `./bin/blinkenkeysd` in a terminal you can watch logs in. Confirm it logs
   enumerating the board.
3. In a second terminal, list devices:
   ```bash
   curl --unix-socket ~/.local/state/blinkenkeys/api.sock http://localhost/devices
   ```
   Confirm the response includes one entry whose name matches the board
   (a `uid-...` name if `VIAL_KEYBOARD_UID` is compiled in, else a
   `5754-c401-...` fallback name).
4. Fetch capabilities for that device name:
   ```bash
   curl --unix-socket ~/.local/state/blinkenkeys/api.sock http://localhost/devices/<name>
   ```
   Confirm `led_count` and `positions` look plausible for the board's known
   matrix.
5. Set key `2,2` to red and confirm it visibly lights up red on the board:
   ```bash
   curl --unix-socket ~/.local/state/blinkenkeys/api.sock \
     -X PUT -d '{"color":"#ff0000"}' \
     http://localhost/devices/<name>/keys/2,2
   ```
6. Repeat step 5 with a named color (`"color":"green"`) and an HSV triple
   (`"color":"0,255,128"`), confirming visibly distinct results each time.
7. With both keys from steps 5–6 still colored, physically unplug and
   replug the board. Confirm both colors reappear automatically within
   ~1-2 seconds, with no new API call — the reconnect-triggered redraw
   (`internal/dispatcher`'s `RedrawReconnected`, driven by `cmd/blinkenkeysd`'s
   1s hotplug-poll loop). Also confirm `GET /devices` shows the *same* name
   as step 3 (not a new `-0`-suffixed name) — this is the untethered-rewire
   path (Task 6), not a fresh allocation.
8. Set one key to a new color, then (without unplugging) wait at least 5
   seconds and confirm nothing visibly changes — the unconditional
   periodic redraw (`RunPeriodicRedraw`, every `dispatcher.RedrawInterval`)
   re-asserting the same color is expected to be a no-op to the eye, not a
   flicker or a reversion.

The 24h untethered-eviction behavior (Task 6) is not practically checkable
manually on this timescale — it's covered by
`TestRegistryReconcileEvictsStaleUntethered` instead. Not this checklist's
job to re-verify.
