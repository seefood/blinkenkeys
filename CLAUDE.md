# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project state

This repo is currently **design-complete, implementation-not-started** for its Go
rewrite. Read `docs/superpowers/specs/2026-09-21-blinkenkeys-phase1-2-design.md`
before writing any code — it is the authoritative spec for the single-binary
`blinkenkeysd` architecture, and this file only summarizes the parts that affect how
you work, not the full rationale. `README.md` has the 6-phase product roadmap; only
Phases 1–2 are speced/in scope right now.

Raw HID / VialRGB Direct protocol ground truth lives in `internal/hid/protocol.go`
(device discovery via usage page `0xFF60`/usage `0x61`, `VIALRGB_SET_MODE`,
`VIALRGB_DIRECT_FASTSET`). Raw HID access to that vendor-defined usage page needs no
macOS Input Monitoring grant (see `CHANGELOG.md`'s Phase 1+2 entries). The implementation
is Go, following the package layout in the design spec (`cmd/blinkenkeysd/`,
`internal/...`).

## Commands

```
make build                        # builds bin/blinkenkeysd
make test                         # go test ./...
make lint                         # prek run --all-files
```

`bin/blinkenkeysd` itself takes `-c`/`--config=<dir>` (default:
`${XDG_CONFIG_HOME:-~/.config}/blinkenkeys`; `config.yaml`, `effects/`,
`templates/` live there) and `--check-config` (validates that directory and
exits, without opening any HID device or listener).

Requires Go 1.27+, a C compiler (cgo — `github.com/sstallion/go-hid` bundles
its own hidapi C sources), and on Linux, the `libudev-dev` headers (hidraw
backend, the default). See the plan's "Verified ground truth" section
(`docs/superpowers/plans/2026-09-23-blinkenkeys-phase1-2-implementation.md`)
for exactly what was checked before relying on it.

Test plan follows the design spec's "Testing" section: unit tests against fake HID
backends and a fake dispatcher; real-hardware checks stay manual/gated (see
`docs/superpowers/manual-checks/`).

## Architecture essentials

(Full detail in the design spec — this is only what you need to not violate the
design while implementing.)

- **One process, `blinkenkeysd`**: no privilege separation. The earlier
  `connectord`/`restd` split assumed macOS's Input Monitoring TCC grant gated raw
  HID access to VialRGB's vendor-defined usage page; that's confirmed false (see
  `CHANGELOG.md`), so there's no privilege boundary to separate.
  `blinkenkeysd` enumerates devices, runs the HTTP server, and owns the dispatcher
  all in one binary — don't reintroduce a second process or an IPC layer between
  them.
- **All access to the open HID handles goes through one dispatcher goroutine** —
  the "traffic cop" that batches flushes into `SetKeys` calls (VialRGB's packet
  ceiling is 9 LEDs per report). The color cache is a frame buffer: `Dispatcher.Write`
  updates it synchronously and queues a colorless flush that reads the cache at
  dispatch time; periodic/reconnect redraw replays it (colors live in the
  keyboard's RAM only and don't survive a reset). HTTP writes go through
  `effects.Engine`, never straight to the dispatcher, so effect supersession is
  enforced in one place. Don't let other goroutines call a `hid.Controller`
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
