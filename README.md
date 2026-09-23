# blinkenkeys

A REST-controllable daemon that drives per-key RGB status indicators on Vial-based
mechanical keyboards, using VialRGB's "Direct" mode (host-controlled, RAM-only LED
colors, independent of the Vial app). Built against the `cxt_studio/12e4` macropad —
see `../README.md` for how that board's firmware got Vial + VialRGB support in the
first place — but designed to generalize to any Vial-capable device.

## Status

Language/implementation: **Go** (see "Why not Python" below). Active spec:
[`docs/superpowers/specs/2026-09-21-blinkenkeys-phase1-2-design.md`](docs/superpowers/specs/2026-09-21-blinkenkeys-phase1-2-design.md)
covers Phases 1–2 (POC → MVP). Phases 3–6 below are roadmap/future-proofing only —
not yet speced in detail, but the concurrency/dispatcher design was made now
specifically so it doesn't require rewrites later.

## Why not Python

`set_key_color.py` (kept in this repo as a working reference) proved the actual
VialRGB Direct protocol end-to-end over raw HID — device discovery, `VIALRGB_SET_MODE`,
`VIALRGB_DIRECT_FASTSET` — using `hidapi`. A Python daemon PoC on macOS was attempted
and abandoned in favor of Go; **the reason originally documented here — that HID
access requires the "Input Monitoring" TCC grant, which a `uv`-managed Python
interpreter invocation can't hold a stable identity for — turned out to be wrong**:
raw HID access to a vendor-defined usage page needs no such grant at all (see the
design spec's Revision history for how this was confirmed). The Go decision stands
regardless: a single static binary with no interpreter/venv dependency is still the
simpler deployment story.

## Roadmap

1. **Set a key's color** — POC. One HID device, one REST call, sets one key/LED to a
   given color (hex, HSV, or named color).
2. **Device enumeration** — MVP. `GET` endpoints report all connected Vial-capable
   devices (up to several at once), their matrix size/LED capabilities, and
   config-assigned stable names (so USB renumbering / port changes don't break
   clients). Duplicate boards get auto-suffixed names (`-0`, `-1`, ...).
3. **Effects** — blink, breathe, two-color alternation, radius-based "explosion"
   propagation from a key, etc. Client requests an effect; `blinkenkeysd` owns the
   animation timing loop.
4. **Abstraction templates** — named semantic states (mic-mute, build-status, DND, ...)
   mapped to phase 3 effects/colors via YAML config dropped in
   `~/.config/blinkenkeys/templates/`.
5. **Per-client key allocation** — a subscriber (e.g. a coding agent instance) is
   allocated a key and directs its own state to it. Open question, unsolved: how to
   correlate a subscriber to a specific terminal tab (iTerm/WezTerm) automatically.
6. **Server-owned timers** — client fires a single "entered state X" event;
   `blinkenkeysd` runs the clock and animates the passage of time itself (motivating
   example: a Claude Code hook says "went idle," and the keyboard animates toward a
   "cache about to expire" warning over the following 5 minutes, entirely
   server-side). Templatable per phase 4's YAML mechanism.

Windows support is an open question intentionally left for a future community PR —
not being built or tested here.

## Networking

`blinkenkeysd` always binds a `$HOME`-owned Unix domain socket (mode `0600`) for
local clients — reachable elegantly from shell/curl via `curl --unix-socket <path>
http://localhost/...` (supported natively since curl 7.40, no extra tooling needed).
It can *optionally* also bind a TCP listener (default `:49994`; e.g. for reaching the
daemon from a remote SSH session back to the machine the keyboard is physically
attached to) — when TCP is enabled, a bearer token is mandatory (not just optional),
since filesystem permissions no longer provide the access control. Plain HTTP + token
is the accepted threat model for now (LAN/trusted-network use); SSH port forwarding
is the documented escape hatch if stronger transport security is ever needed, rather
than adding TLS to `blinkenkeysd` itself.

## Known limitations

VialRGB Direct-mode colors (`g_direct_mode_colors`) live in RAM only and are lost on
any firmware reset, USB replug, or brownout — the daemon has no way to read the
device's current LED state back, only to (re)assert what it should be. This is why
the design keeps a host-side cache of last-set colors and periodically re-asserts it
(see the design spec).
