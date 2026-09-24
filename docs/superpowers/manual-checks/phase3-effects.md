# Phase 3 effects/templates hardware check

Manual, gated on physical hardware — not run in CI. Run after any change touching
`internal/effects`, `internal/dispatcher`, `internal/api`, or `cmd/blinkenkeysd`.

Prerequisites: as in `phase1-2-hardware-roundtrip.md` (`cxt_studio/12e4` attached,
`personal/vialrgb-direct/001-enable` firmware). No other process may hold the HID
device — stop any running `blinkenkeysd` first.

Shorthand used below:

```bash
SOCK=~/.local/state/blinkenkeys/api.sock
bk() { curl -s -w '%{http_code}\n' --unix-socket "$SOCK" -X PUT -d "$2" "http://localhost/devices/0/keys/$1"; }
```

1. `make build`, then
   `./bin/blinkenkeysd -config-dir=examples/config -check-config` → `ok`.
2. Start `./bin/blinkenkeysd -config-dir=examples/config`. Confirm a
   "new device seen" log line naming the board, with a `devices:` snippet.
3. **Timer stages:** `bk 0,0 '{"state":"claude/idle"}'` → `204`. Key is green for
   3 minutes, then alternates mostly-green, then mostly-red after minute 4, then
   solid red at 5:00.
4. **Supersession:** while it runs, `bk 0,0 '{"state":"claude/working"}'` → key
   breathes blue immediately, with no red flash from the old timer's final state.
5. **Addressing:** `bk led:0 '{"color":"#ffffff"}'` and `bk idx:0 '{"color":"#ff00ff"}'`
   → 204 and visible; `bk esc '{"color":"red"}'` → `501`; `bk 9,9 '{"color":"red"}'` → `404`.
6. **Unplug mid-timer:** start `claude/idle`, unplug before 3:00, replug after
   4:00. Within ≤5 s the key shows the *current* stage (alternating), not green.
7. **Pre-declared device:** stop the daemon, put the logged snippet from step 2 into
   `examples/config`-copy `config.yaml` (use a temp copy, e.g.
   `cp -r examples/config /tmp/bkcfg`), unplug the board, and start with
   `-config-dir=/tmp/bkcfg`. `bk 0,0 '{"color":"#00ffff"}'` → `204`;
   `GET /devices/0` → `503`. Plug in → key turns cyan within ~1 s.
