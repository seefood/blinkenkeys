# Claude Code integration

Hook examples wiring Claude Code session state into `blinkenkeysd`. This
directory will grow as more hook-driven integrations are added — these are
the first two.

## Prerequisites

1. `blinkenkeysd` built and running (`make build && bin/blinkenkeysd`), with
   `examples/config/effects/` and `examples/config/templates/` copied (or
   symlinked) into `${XDG_CONFIG_HOME:-~/.config}/blinkenkeys/`.
2. Find your device's stable name:
   ```
   curl -s --unix-socket "$HOME/.local/state/blinkenkeys/api.sock" http://localhost/devices
   ```
   Every command below is hardcoded to `uid-440d6644c3b0438c` — replace it
   with your own device's `name` from that response.
3. Merge the `hooks` object from one of the two JSON files below into your
   `.claude/settings.json` (or `.claude/settings.local.json` for a
   machine-local, not-checked-in setup).

## Why `claude/idle` and `claude/waiting` both use `timer5min`

See the top-level `README.md`'s "Scratching my itch" section for the full
motivation: on the $20/mo Claude subscription tier, the prompt cache has a
5-minute TTL. Once it expires, your next prompt costs full price instead of
the ~10% cached rate — so the thing worth knowing at a glance isn't just
"is Claude idle," it's "how much of that 5-minute window is left."

`examples/config/effects/timer5min.yaml` encodes that countdown directly as
an LED animation: solid green for the first 3 minutes (cache is fresh, no
rush), alternating green/red for the next minute (getting close), a faster,
more red-heavy alternation for the last minute (act now), then solid red
once the cache is dead. `examples/config/templates/claude.yaml` maps both
`idle` (turn ended, waiting for your next prompt) and `waiting` (blocked on
a permission prompt) to this same effect — from the cache's perspective
they're identical: no API call is in flight, so the clock is ticking either
way. `working` gets its own `breathe_orange` effect instead, since the cache
question doesn't apply while a request is in flight.

## `hooks-basic.json`

One LED per live Claude Code session. `SessionStart`/`Stop` set
`claude/idle` (starts the cache-expiry countdown), `UserPromptSubmit` sets
`claude/working`, `PermissionRequest` sets `claude/waiting`, `SessionEnd`
releases the claim (blanking the key). Keys are auto-assigned from the
device's row-1-and-below pool — see the Phase 3 spec's "Named-key model"
section — so you don't pick a key yourself; a session just claims the next
free one and keeps it (refreshed on every write, released after 8h idle or
on `SessionEnd`).

## `hooks-wezterm-pane.json`

Everything `hooks-basic.json` does, plus a second write per event to a
row-0 key addressed *directly* by matrix position (`R,C`), whose column is
derived from WezTerm's `$WEZTERM_PANE` env var
(`0,$(( WEZTERM_PANE % 4 ))` — `4` because the reference device has 4 keys
in row 0; adjust to your own device's row-0 width). This gives you a
second, per-*pane* indicator alongside the per-*session* one from
`hooks-basic.json`: glance at row 0 to see which terminal tab has something
going on, without needing to know which session claimed which pooled key.

Every pane command is guarded with `[ -n "$WEZTERM_PANE" ] && ...`, so
outside WezTerm (SSH, tmux, a plain terminal) it's a silent no-op and only
the row-1+ per-session behavior applies.

Two things to know before using this one:

- **`$WEZTERM_PANE` is a mux-server-lifetime counter, not a small stable
  slot index** — it climbs monotonically and is never reused as panes open
  and close, confirmed by spawning and closing a throwaway pane against a
  live `wezterm cli list`. `% 4` folds that onto the device's 4 row-0 keys,
  which means two panes whose IDs differ by a multiple of 4 collide and
  silently overwrite each other's key. There's no claim/ownership check on
  this path (it's a direct address, not a pooled name), so this is a known,
  accepted trade-off, not a bug.
- **`SessionEnd` can't `DELETE` the row-0 key.** `DELETE
  /devices/{name}/keys/{key}` only releases a *named* claim (see
  `internal/api/handlers.go`); a direct `R,C` address has no claim to
  release, so `SessionEnd`'s pane-row command instead `PUT`s `#000000` to
  blank it directly. Unlike the per-session key, the pane key is not
  otherwise released when the session ends — it's tied to the *pane*, which
  usually outlives any one `claude` invocation, so the next `claude` run in
  that same pane just claims/overwrites it again.
