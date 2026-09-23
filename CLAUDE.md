# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project state

This repo is currently **design-complete, implementation-not-started** for its Go
rewrite. Read `docs/superpowers/specs/2026-09-21-blinkenkeys-phase1-2-design.md`
before writing any code — it is the authoritative spec for the single-binary
`blinkenkeysd` architecture, and this file only summarizes the parts that affect how
you work, not the full rationale. `README.md` has the 6-phase product roadmap; only
Phases 1–2 are speced/in scope right now.

The only code that currently exists is `set_key_color.py` — a working,
hardware-validated reference for the raw HID / VialRGB Direct protocol (device
discovery via usage page `0xFF60`/usage `0x61`, `VIALRGB_SET_MODE`,
`VIALRGB_DIRECT_FASTSET`). Treat it as the protocol ground truth when implementing
the Go `internal/hid` package, not as code to build on top of. The Python daemon
scaffold that used to sit alongside it (`pyproject.toml` / `uv.lock` /
`.python-version` / `src/vialrgb_notify/`) has been removed — that PoC hit a dead
end on macOS (see "Why not Python" in README.md: the Input Monitoring TCC grant
requires a stable code-signing identity that an ad-hoc-signed `uv`-managed
interpreter invocation doesn't have) and is superseded by the Go rewrite. **That
original reason turned out to be wrong** — raw HID access to a vendor-defined usage
page needs no Input Monitoring grant at all (see the design spec's Revision
history) — but the Go decision itself stands regardless. The real implementation
will be Go, following the package layout in the design spec (`cmd/blinkenkeysd/`,
`internal/...`), none of which exists yet.

## Commands

```
uv run --with hidapi set_key_color.py    # protocol smoke test against real hardware
```

`set_key_color.py` has no `pyproject.toml` of its own anymore; `uv run --with` builds
a throwaway env for its one dependency (`hidapi`) without needing one. Requires the
`cxt_studio/12e4` board attached with the `personal/vialrgb-direct/001-enable`
firmware branch flashed (see `../qmk_vial` and `../README.md` in the parent
`CXT-studio` tree for how that firmware was built). On macOS this also requires the
Input Monitoring TCC grant for whatever binary runs it — expect this to fail
intermittently for exactly the code-signing-identity-instability reason documented
above; that instability is *why* the real daemon is being written in Go.

No lint/test tooling is configured yet (no linter config, no test files). Once Go
implementation starts, follow the test plan in the design spec's "Testing" section
(unit tests against fake HID backends and a fake dispatcher; real-hardware checks
stay manual/gated).

## Architecture essentials

(Full detail in the design spec — this is only what you need to not violate the
design while implementing.)

- **One process, `blinkenkeysd`**: no privilege separation. The earlier
  `connectord`/`restd` split assumed macOS's Input Monitoring TCC grant gated raw
  HID access to VialRGB's vendor-defined usage page; that's confirmed false (see the
  design spec's Revision history), so there's no privilege boundary to separate.
  `blinkenkeysd` enumerates devices, runs the HTTP server, and owns the dispatcher
  all in one binary — don't reintroduce a second process or an IPC layer between
  them.
- **All access to the open HID handles goes through one dispatcher goroutine** —
  the "traffic cop" that does per-device batching of `SetKey` calls into `SetKeys`
  (VialRGB's packet ceiling is 9 LEDs per report) and owns the last-known-good color
  cache used for reconnect/periodic redraw (colors live in the keyboard's RAM only
  and don't survive a reset). Don't let other goroutines call a `hid.Controller`
  directly.
- **Never call `syscall.Setuid`/`Setgid` on a running process to drop privilege** —
  it's broken across Go's OS threads (`golang/go#1435`). There's no spawned child to
  drop privilege for in the single-binary design, so this only matters as a reason
  `blinkenkeysd` can never safely de-escalate itself out of a root fallback either —
  it just logs a warning (`warnIfRootFallback`) instead.
- **macOS: `blinkenkeysd` runs as a normal user process (LaunchAgent), never a
  LaunchDaemon or root.** No special signing-identity requirement remains now that
  no TCC grant is needed for raw HID to this usage page — a plain build works.
- **Linux: prefer a udev rule over running `blinkenkeysd` as root.** Root is a
  fallback only, for environments where a udev rule can't be installed — and since
  there's no unprivileged process left to isolate the HTTP surface behind, that
  fallback runs the *entire* daemon, listeners included, as root. Log a prominent
  warning in that case rather than doing it quietly.
- **Color values passed to the firmware are QMK's native HSV**: 0–255 for each of H/S/V,
  not the 0–360°/0–100%/0–100% convention. The REST API's `color` field accepts hex,
  an `"H,S,V"` triple in that 0–255 scale, or a CSS/X11 color name — all normalized by
  one shared parser that Phase 4's YAML templates will also reuse.
- **Bearer token is optional on the Unix-socket listener, mandatory on the TCP
  listener** (not user-configurable to disable) — filesystem permissions provide the
  access control on the socket; nothing does on the network.
