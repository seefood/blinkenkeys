# blinkenkeys: Phase 1+2 design (POC → MVP)

Date: 2026-09-21 (architecture revised 2026-09-23: dropped privilege separation
after confirming raw HID to a vendor-defined usage page needs no macOS TCC grant;
see `CHANGELOG.md` at the repo root.)

## Scope

This spec covers **Phase 1** (set a single key's color on one HID device) and
**Phase 2** (enumerate connected devices, report capabilities, stable naming),
combined into one implementation cycle because both require the same
multi-device-aware connector architecture. Phases 3–6 (effects, templates,
per-client key allocation, server-owned timers — see `../../README.md` for the
full roadmap) are explicitly **out of scope for implementation** here, but several
architectural decisions below are made now, ahead of need, specifically so those
phases don't require rewrites. Each such decision is marked **(forward-looking)**.

## Background / constraints

- Target hardware: any keyboard running QMK with VIALRGB support. initial testing will be `cxt_studio/12e4` (VID `0x5754` / PID `0xC401`), VialRGB Direct
  mode enabled per `qmk_vial` branch `personal/vialrgb-direct/001-enable`. Protocol
  already validated end-to-end by `set_key_color.py` (raw HID, usage page `0xFF60` /
  usage `0x61`, `VIALRGB_SET_MODE` / `VIALRGB_DIRECT_FASTSET`).
  The goal is to support any compatible keyboard running a similar firmware config.
- A Python PoC was attempted first and abandoned in favor of Go; **the originally
  documented reason (macOS Input Monitoring TCC gating raw HID) turned out to be
  wrong** — see "macOS specifics" below and `CHANGELOG.md`. The Go decision
  itself is not reopened here: a single static binary with no interpreter/venv
  dependency is still the simpler deployment story, but the "Why not Python"
  rationale in the README needs updating separately from this spec.
- macOS specifics (verified, not assumed — see `CHANGELOG.md` for how):
  - Raw HID access to a **vendor-defined usage page** (`0xFF60`/`0x61` here) does
    **not** require the Input Monitoring TCC grant. Confirmed three ways: (1) Vial's
    own `vial-gui` source (`src/main/python/util.py`, `is_rawhid()`) explicitly skips
    all permission probing on macOS/Windows with the comment "there's no reason to
    check for permission issues on mac or windows"; (2) `TCC.db`'s `access` table has
    zero rows for `kTCCServiceListenEvent` on the test machine, for any app,
    including ones that do use it (BetterTouchTool, Karabiner-Elements); (3)
    `set_key_color.py`, run via an ad-hoc-signed `uv run` invocation with no prior
    grant, opened the device and set a color with no prompt and no error (once no
    other process, e.g. Vial.app, already held the handle open — macOS HID opens are
    exclusive, and that's a distinct failure mode from a permission denial).
  - Input Monitoring gates the **standard-usage-page, system-wide** input APIs
    (`CGEventTapCreate`, `IOHIDManager` against usage page `0x01`/usage `0x06`
    generic-desktop-keyboard) — i.e. global keystroke/mouse-event monitoring, not a
    targeted `IOHIDDeviceOpen` against one vendor-defined interface on one device.
  - Practical consequence: **no privileged/elevated component is needed on macOS at
    all.** The daemon runs as a normal user process (LaunchAgent), same as any other
    app, and opens the HID device directly.
- Linux specifics (unchanged by the above — this is a separate, real permission
  boundary):
  - `/dev/hidraw*` is normally root-owned; the correct mechanism is a **udev rule**
    scoping access to this device's exact VID/PID for the logged-in user (e.g.
    `TAG+="uaccess"` for the modern systemd/logind seat-based grant), not running the
    daemon as root.
  - SUID-root was considered and rejected: Go's `syscall.Setuid`/`Setgid` only affect
    the calling OS thread, not the whole process (`golang/go#1435`) — a Go program
    cannot safely drop root privilege after acquiring it, so a SUID binary would need
    to stay root for its entire runtime. Combined with `nosuid`-mount gotchas (the bit
    is silently ignored on such mounts), SUID was dropped in favor of the udev rule
    (primary path) with a root fallback only when no udev rule can be installed — see
    "Architecture" for what running the (now single-process) daemon as root implies
    in that fallback case.
- Windows is explicitly out of scope for this implementation (left for a future
  community PR); the architecture below doesn't preclude it.

## Architecture

**One long-lived process, `blinkenkeysd`.** The Phase 1 design split this into a
privileged `connectord` + unprivileged `restd` pair specifically to isolate raw HID
access behind a stable-identity, always-user-level component, on the assumption that
macOS gated that access. Now that's confirmed false (see Background) — there is no
privilege boundary to separate in the first place on macOS, and on Linux the udev
rule already grants the *whole* logged-in user's session access to the device, not
just a specially-blessed process. Splitting the process bought nothing and cost an
IPC layer, a supervision relationship, and a second binary to build/deploy/log. It's
removed.

`blinkenkeysd` responsibilities, all in one process:

- Enumerates all matching Vial raw-HID interfaces (usage page `0xFF60` / usage
  `0x61`) at startup and on hotplug, and opens/talks to them directly via `hidapi`
  (no IPC hop).
- Resolves each device to a stable identity per configured naming rule (user's
  choice, per device): Vial UID (`VIAL_KEYBOARD_UID`, survives port changes) → VID/PID
  + USB path → VID/PID only. Collisions (two devices resolving to the same name, e.g.
  two identical boards) get deduplicated with `-0`, `-1`, `-2` ... suffixes in
  enumeration order.
- HTTP server (listeners, routes, auth) — unchanged from the original `restd` design,
  see "External API" below.
- Owns the single-writer "dispatcher" that serializes all access to the HID handle
  (see Concurrency) — same rationale as before (hidapi is not guaranteed safe under
  concurrent writes to the same handle), just with the dispatcher now writing to the
  HID handle directly instead of to a socketpair.
- Platform-specific privilege model:
  - **Linux**: runs as the normal user if a udev rule granting device access is
    installed (documented, primary path). Falls back to running the entire daemon —
    HTTP listeners included — as root only if no such rule is present; this is a
    strictly worse security posture than the old split (the HTTP surface is now also
    root), so the udev rule is the strongly recommended path and the root fallback
    should log a prominent warning on startup.
  - **macOS**: runs as the normal user via a LaunchAgent (never a LaunchDaemon/root).
    No special signing-identity requirement remains once no TCC grant is needed for
    it — a plain build works, but a stable identity is still nice to have to avoid
    Gatekeeper re-prompting after every rebuild on a Developer Program-enrolled
    machine.
  - **Windows**: unimplemented; left as an open question for a future contribution.

## External API: `blinkenkeysd` ↔ clients

- Listeners (config-driven):
  - `$HOME`-owned Unix socket, mode `0600`, always on. Bearer token optional here
    (filesystem permissions already provide the access control).
  - Optional TCP listener, off by default, bindable to a configured interface/port
    (LAN use case: reaching the daemon from a remote SSH session back to the machine
    the keyboard is attached to). **Bearer token is mandatory whenever TCP is
    enabled** — not user-configurable to disable, since nothing else scopes access
    once it's network-reachable. Plain HTTP (no TLS) is the accepted threat model for
    a trusted LAN; SSH port forwarding is the documented path if stronger transport
    security is ever wanted, rather than adding TLS to `blinkenkeysd`.
- Routes (Phase 1 + 2):
  - `PUT /devices/{name}/keys/{row},{col}` — body: a `color` field (see Color format).
  - `GET /devices` — list configured/connected device names + connection state.
  - `GET /devices/{name}` — capabilities: matrix dimensions, LED count, LED positions
    (via the existing `VIALRGB_GET_NUMBER_LEDS` / `VIALRGB_GET_LED_INFO` calls).

Reachable via:

```
curl --unix-socket ~/.local/state/blinkenkeys/api.sock \
  -X PUT -d '{"color":"#ff0000"}' \
  http://localhost/devices/cxt12e4-0/keys/2,2
```

`--unix-socket` has been part of curl since 7.40; the `http://localhost/...` URL is
still required (curl needs it for the `Host:` header, path, and method) even though
the transport is a Unix socket rather than TCP.

## Color format

Single `color` field on the `PUT` body, accepting three forms, all resolved by one
shared parser (reused unchanged by the Phase 4 YAML templates later):

1. `"#rrggbb"` — standard hex RGB.
2. `"H,S,V"` — three comma-separated integers, **0–255 each**, matching QMK's native
   `HSV` type exactly (`quantum/color.h`; `vialrgb.c` passes these bytes straight
   through to `rgb_matrix_sethsv_noeeprom`) — not the more common 0–360°/0–100%/0–100%
   convention, to avoid a lossy rescale on the hot path.
3. A named color, resolved against the standard CSS/X11 color keyword set (~148
   names) — not a custom palette. Note: CSS `"green"` is `#008000` (unusually dark);
   this is the standard's known quirk, not something this project alters.

Detection order: leading `#` → hex; parses as three comma-separated integers → HSV
triple; else → keyword lookup. Anything else → `400`.

## State persistence & refresh (forward-looking, built now)

VialRGB Direct-mode colors (`g_direct_mode_colors`) live in the keyboard's RAM only —
confirmed on real hardware: a firmware reset, USB replug, or brownout silently
reverts every LED to whatever the boot-time mode/colors are, and there is no HID
command to read the current Direct-mode color array back. `blinkenkeysd` is the only
place that can know what a device's colors are "supposed to be," so it must remember
it, not just fire-and-forget each `PUT`.

- **Cache**: an in-memory `map[deviceID]map[ledIndex]HSV` of the last color
  successfully written to each key, updated after every successful `SetKey`/`SetKeys`
  call. Not persisted to disk — a process restart has no more idea of true device
  state than the device itself does after a reset, so there's nothing meaningful to
  reload; the cache starts empty and is rebuilt from subsequent `PUT`s.
- **Reconnect-triggered redraw**: when device enumeration detects a device
  transitioning from absent/disconnected to present (including a bare USB replug,
  not just an explicit API-visible event), immediately replay that device's full
  cached color set as one batched `SetKeys` call (or several, respecting the 9-LED
  packet ceiling — see Concurrency/batching).
- **Untethered rewire, and 24h cache eviction**: refines the above — a device that
  disconnects doesn't lose its name/cache immediately; it goes `untethered` (cache
  retained, disconnect time recorded) rather than freed. A later device that resolves
  to the same identity rewires into that `untethered` entry (triggering the
  reconnect-redraw above) rather than getting a fresh name/empty cache — unless an
  identity-matching device is already `connected`, in which case the newcomer never
  steals that slot and gets its own fresh dedup-suffixed name instead. An `untethered`
  entry older than 24 continuous hours is evicted (cache cleared, name freed); a
  later match past that point is a fresh allocation, not a rewire.
- **Unconditional periodic redraw**: independent of reconnect detection, replay every
  device's full cached color set every 5 seconds, always. This is deliberately
  redundant with the reconnect-triggered redraw — it's the fallback for the case
  where a reconnect is missed, mis-detected, or the device resets without a full
  USB re-enumeration (e.g. a firmware-side soft reset that doesn't drop the USB
  connection). **Assumption, flag if wrong**: "either... or... either way" in the
  originating request is read as "do both, the 5s timer is a safety net," not "pick
  one."
- Redraws go through the same dispatcher/batching path as any other `SetKeys` call
  (see Concurrency) — no special-cased write path.
- Devices with an empty cache (never had a color set since `blinkenkeysd` started) are
  skipped on both triggers — there's nothing to redraw yet.

## Concurrency & traffic management (forward-looking, built now)

Even Phase 1 has multiple concurrent HTTP requests potentially racing on the single
HID handle, so this is not deferred to Phase 3.

- **Single dispatcher goroutine** owns the HID handle exclusively. Every other
  goroutine (HTTP handlers now; the periodic-redraw timer above; per-key animation
  timers in Phases 3/6, per-client subscriptions in Phase 5) submits a request
  through a buffered channel and blocks on its own per-request reply channel — never
  touches the handle directly.
- **Batching**: before sending, the dispatcher non-blockingly drains any other
  already-queued requests and coalesces same-device, contiguous-index `SetKey` calls
  into one `SetKeys` RPC — up to VialRGB's own per-packet ceiling of 9 LEDs (32-byte
  report, 27 usable bytes ÷ 3 bytes/LED, per `vialrgb.c`'s `fast_set_leds` comment).
  Requests for different devices, or overflow past 9 keys, become multiple sequential
  writes within the same drain cycle. **Phase 1 note**: the batching code path exists
  and is tested from the start, but with only single-key `PUT` requests plus the
  redraw paths above, it will rarely have more than a handful of items to batch —
  this is intentional groundwork, not dead code, for when Phase 3+ generates many
  concurrent per-key updates.
- **Backpressure**: bounded request channel (e.g. depth 64). Full → immediate `503`
  rather than unbounded goroutine/memory growth. Read/write deadline on the HID
  handle (e.g. 1s) so a wedged/unplugged device can't hang the dispatcher forever.
- **Partial-batch failure**: batches are per-device by construction, so a device that
  disconnects mid-batch fails only that batch; other devices' pending writes are
  unaffected.
- **Threading model, explicitly**: not single-threaded overall. Normal
  goroutine-per-request HTTP handling (and later, per-key animation timers) run
  concurrently. Serialization is narrowly scoped to the dispatcher + the HID handle —
  the one place it's actually required.

## Error handling

| Condition | Status |
|---|---|
| Malformed `color` string, out-of-range `row,col` | 400 |
| Unknown device name, or key not present on that device's matrix | 404 |
| Missing/invalid bearer token when required | 401 / 403 |
| Named device currently disconnected | 503 |
| Dispatcher queue full (backpressure) | 503 |

## Logging

- `log/slog`, one stream (one process now).
- Standard debug/info/warn/error. Debug includes raw HID report bytes (opt-in),
  replacing today's informal prints in `set_key_color.py`.
- **Hard rule**: bearer tokens are never logged at any level — log
  "token present/absent/invalid", never the value.

## Config

`~/.config/blinkenkeys/config.yaml` (path TBD-but-conventional; not a placeholder
in the sense of being undecided-and-blocking, just not bikeshedded here). Naming
rules, listener config, and tokens all live here, loaded once at `blinkenkeysd` startup.
Phase 4's per-status YAML templates live under a `templates/` subdirectory of the
same config root.

## Testing

- Enumeration/identity-resolution/dedup logic: unit tests against a fake HID
  backend — no physical board required for most of it (protocol correctness is
  already established by `set_key_color.py` against real hardware).
- HTTP handlers, auth middleware, and the color parser (including all three formats'
  happy paths and the `400` cases): unit tests against a fake dispatcher.
- Dispatcher/batching: unit tests asserting N concurrent same-device contiguous
  `SetKey`s collapse into one `SetKeys` call, different-device calls never merge, and
  a full queue returns `503` (inject a slow fake HID backend).
- State persistence: unit tests asserting the cache updates on successful writes,
  reconnect detection triggers a full redraw from cache, and the periodic timer fires
  redraws on its own independent of reconnect events.
- Manual/gated: real-hardware round-trip check, documented but not automated (CI has
  no physical keyboard attached).

## Package layout (Go, indicative)

```
blinkenkeys/
  cmd/blinkenkeysd/         main() — startup, listeners, dispatcher, handlers
  internal/hid/         enumeration, identity resolution, VialRGB protocol
  internal/api/         HTTP handlers, color parsing, auth middleware
  internal/dispatcher/  traffic-cop/batching logic, state cache, redraw timers
  config/               YAML loading (Phase 4+ template schema lands here later)
```

The existing Python scaffold (`pyproject.toml`, `uv.lock`, `.python-version`,
`src/vialrgb_notify/`) is superseded by the above and not part of this design; it is
left in place for now as historical record of the abandoned Python attempt.
`set_key_color.py` is kept as a working protocol reference, not superseded — it
remains the from-first-principles proof that the HID/VialRGB protocol works,
independent of language choice.

## Explicitly out of scope for Phase 1+2

Effects/animation rendering (Phase 3), YAML status templates (Phase 4), per-client
key allocation and terminal-tab correlation (Phase 5), server-owned timers (Phase 6),
Windows support, TLS on the TCP listener. See `../../README.md` for the roadmap.
