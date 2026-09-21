# vialrgb-notify: Phase 1+2 design (POC → MVP)

Date: 2026-09-21

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

- Target hardware: `cxt_studio/12e4` (VID `0x5754` / PID `0xC401`), VialRGB Direct
  mode enabled per `qmk_vial` branch `personal/vialrgb-direct/001-enable`. Protocol
  already validated end-to-end by `set_key_color.py` (raw HID, usage page `0xFF60` /
  usage `0x61`, `VIALRGB_SET_MODE` / `VIALRGB_DIRECT_FASTSET`).
- A Python PoC was attempted first and abandoned: macOS's Input Monitoring TCC grant
  is tied to a stable code-signing identity, which a `uv`-managed Python interpreter
  invocation does not have (ad-hoc signed, no Team ID). No working PoC was achievable
  under Python for this reason. **Decision: Go**, compiled to a single binary that can
  hold one consistent signing identity across rebuilds.
- macOS specifics (verified, not assumed):
  - Root/sudo does **not** bypass TCC (Input Monitoring, Accessibility, Screen
    Recording, Full Disk Access) — enforced since OS X 10.11 specifically because
    "run as root" is the obvious malware bypass otherwise.
  - TCC gates by **code identity** (the binary's signature), not by UID.
  - A LaunchDaemon (root, no GUI session) cannot receive the Input Monitoring consent
    dialog at all — it's not merely unprivileged, it's structurally unable to be
    granted. The privileged/gated component must run as a **LaunchAgent under the
    logged-in user**.
  - A self-signed certificate (created free via Keychain Access, no paid Apple
    Developer Program membership required) is sufficient to give a locally-built,
    locally-run binary a **stable** signing identity, so a one-time GUI consent grant
    persists across rebuilds. Ad-hoc signing (Go's default on darwin/arm64, via its
    internal `cmd/internal/codesign`) does not persist reliably, since it carries no
    Team Identifier and TCC ends up tracking by code digest alone.
  - Apple Developer Program membership ($99/yr) is only required for notarization /
    Gatekeeper distribution to *other* machines — irrelevant here since nothing is
    downloaded (`com.apple.quarantine` never gets set on a locally-built binary).
- Linux specifics:
  - `/dev/hidraw*` is normally root-owned; the correct mechanism is a **udev rule**
    scoping access to this device's exact VID/PID for the logged-in user (e.g.
    `TAG+="uaccess"` for the modern systemd/logind seat-based grant), not running the
    daemon as root.
  - SUID-root was considered and rejected: Go's `syscall.Setuid`/`Setgid` only affect
    the calling OS thread, not the whole process (`golang/go#1435`) — a Go program
    cannot safely drop root privilege after acquiring it, so a SUID binary would need
    to stay root for its entire runtime. Combined with `nosuid`-mount gotchas (the bit
    is silently ignored on such mounts), SUID was dropped in favor of the udev rule
    (primary path) with a root fallback only when no udev rule can be installed.
- Windows is explicitly out of scope for this implementation (left for a future
  community PR); the architecture below doesn't preclude it.

## Architecture

Two long-lived processes:

### `connectord` (privileged, minimal, rarely changes)

- Only process that ever touches `/dev/hidraw*` (Linux) or `IOHIDDeviceOpen` (macOS).
- Enumerates all matching Vial raw-HID interfaces (usage page `0xFF60` / usage `0x61`)
  at startup and on hotplug.
- Resolves each device to a stable identity per configured naming rule (user's
  choice, per device): Vial UID (`VIAL_KEYBOARD_UID`, survives port changes) → VID/PID
  + USB path → VID/PID only. Collisions (two devices resolving to the same name, e.g.
  two identical boards) get deduplicated with `-0`, `-1`, `-2` ... suffixes in
  enumeration order.
- Platform-specific privilege model:
  - **Linux**: runs as the normal user if a udev rule granting device access is
    installed (documented, primary path). Falls back to root only if no such rule is
    present.
  - **macOS**: runs as the normal user via a LaunchAgent (never a LaunchDaemon/root —
    see Background above). Signed with a stable (self-signed is sufficient) identity.
  - **Windows**: unimplemented; left as an open question for a future contribution.
- At startup, creates an anonymous `socketpair(AF_UNIX, SOCK_STREAM)`, keeps one fd,
  and spawns `restd` as a **child process**, passing the other fd via
  `exec.Cmd.ExtraFiles` (no filesystem path for this channel — see IPC below).
  - If `connectord` is running as root (Linux fallback case only), it drops privilege
    for the `restd` child via `exec.Cmd.SysProcAttr.Credential{Uid, Gid}` at spawn
    time — safe because this sets the credential before the child's own runtime
    starts, unlike the unsafe in-process `setuid` pattern ruled out above. In the
    common case (macOS, or Linux with the udev rule), both processes already run as
    the same uid and no privilege drop happens.
  - Acts as `restd`'s minimal supervisor (Phase 1: restart-together-on-exit; smarter
    supervision can come later).
- Internal RPC surface exposed to `restd` (see IPC below): `SetKey`, `SetKeys`
  (batched — **forward-looking**, see Concurrency), `ListDevices`, `GetCapabilities`.

### `restd` (unprivileged, everything else)

- HTTP server. Never touches hardware directly — every device operation goes through
  `connectord` over the internal link.
- Listeners (config-driven):
  - `$HOME`-owned Unix socket, mode `0600`, always on. Bearer token optional here
    (filesystem permissions already provide the access control).
  - Optional TCP listener, off by default, bindable to a configured interface/port
    (LAN use case: reaching the daemon from a remote SSH session back to the machine
    the keyboard is attached to). **Bearer token is mandatory whenever TCP is
    enabled** — not user-configurable to disable, since nothing else scopes access
    once it's network-reachable. Plain HTTP (no TLS) is the accepted threat model for
    a trusted LAN; SSH port forwarding is the documented path if stronger transport
    security is ever wanted, rather than adding TLS to `restd`.
- Owns the single-writer "dispatcher" that serializes all access to the internal link
  (see Concurrency).
- Routes (Phase 1 + 2):
  - `PUT /devices/{name}/keys/{row},{col}` — body: a `color` field (see Color format).
  - `GET /devices` — list configured/connected device names + connection state.
  - `GET /devices/{name}` — capabilities: matrix dimensions, LED count, LED positions
    (via `connectord`'s `GetCapabilities`, itself backed by the existing
    `VIALRGB_GET_NUMBER_LEDS` / `VIALRGB_GET_LED_INFO` calls).

### Internal IPC: `connectord` ↔ `restd`

**Anonymous `socketpair`, not a named filesystem socket.** `connectord` creates the
pair before spawning `restd` and passes one end as an inherited fd at process-spawn
time. Nothing is ever written to disk for this channel — there is no path for a third
process to open, race, symlink-attack, or misconfigure permissions on. This is the
same pattern used by privilege-separated daemons like OpenSSH's monitor/child split.

Protocol on this link is **strictly synchronous**: `restd`'s dispatcher sends one
request, blocks until `connectord`'s response, then proceeds. No correlation IDs are
needed as a result. `connectord` needs no dispatcher of its own — it has exactly one
client sending one request at a time, which is also what keeps concurrent access to
the underlying HID handle from ever happening (hidapi is not guaranteed safe under
concurrent writes to the same handle).

Framing: length-prefixed JSON (simple, adequate for this message volume; msgpack is a
drop-in future optimization if needed, not decided now since it doesn't affect the
architecture).

### External API: `restd` ↔ clients

Named `$HOME`-owned Unix socket (see Listeners above), reachable via:

```
curl --unix-socket ~/.local/state/vialrgb-notify/api.sock \
  -X PUT -d '{"color":"#ff0000"}' \
  http://localhost/devices/cxt12e4-0/keys/2,2
```

`--unix-socket` has been part of curl since 7.40; the `http://localhost/...` URL is
still required (curl needs it for the `Host:` header, path, and method) even though
the transport is a Unix socket rather than TCP.

## Color format

Single `color` field on the `PUT` body, accepting three forms, all resolved by one
shared parser in `restd` (reused unchanged by the Phase 4 YAML templates later):

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

## Concurrency & traffic management (forward-looking, built now)

Even Phase 1 has multiple concurrent HTTP requests potentially racing on the single
internal link, so this is not deferred to Phase 3.

- **Single dispatcher goroutine** in `restd` owns the socketpair connection
  exclusively. Every other goroutine (HTTP handlers now; per-key animation timers in
  Phases 3/6, per-client subscriptions in Phase 5) submits a request through a
  buffered channel and blocks on its own per-request reply channel — never touches
  the connection directly.
- **Batching**: before sending, the dispatcher non-blockingly drains any other
  already-queued requests and coalesces same-device, contiguous-index `SetKey` calls
  into one `SetKeys` RPC — up to VialRGB's own per-packet ceiling of 9 LEDs (32-byte
  report, 27 usable bytes ÷ 3 bytes/LED, per `vialrgb.c`'s `fast_set_leds` comment).
  Requests for different devices, or overflow past 9 keys, become multiple sequential
  writes within the same drain cycle. **Phase 1 note**: the batching code path exists
  and is tested from the start, but with only single-key `PUT` requests in Phase 1,
  it will rarely have more than one item to batch — this is intentional groundwork,
  not dead code, for when Phase 3+ generates many concurrent per-key updates.
- **Backpressure**: bounded request channel (e.g. depth 64). Full → immediate `503`
  rather than unbounded goroutine/memory growth. Read/write deadline on the socketpair
  (e.g. 1s) so a wedged `connectord` can't hang the dispatcher forever; a timeout here
  surfaces as `503` and should eventually trigger respawn/health-check logic (a
  lightweight two-way heartbeat so `restd` can detect a hung-but-alive `connectord` is
  a nice-to-have, not required for Phase 1).
- **Partial-batch failure**: batches are per-device by construction, so a device that
  disconnects mid-batch fails only that batch; other devices' pending writes are
  unaffected.
- **Threading model, explicitly**: not single-threaded overall. Normal
  goroutine-per-request HTTP handling (and later, per-key animation timers) run
  concurrently. Serialization is narrowly scoped to the dispatcher + the synchronous
  `connectord` link — the one place it's actually required.

## Error handling

| Condition | Status |
|---|---|
| Malformed `color` string, out-of-range `row,col` | 400 |
| Unknown device name, or key not present on that device's matrix | 404 |
| Missing/invalid bearer token when required | 401 / 403 |
| Named device currently disconnected, or `connectord` unreachable | 503 |
| Dispatcher queue full (backpressure) | 503 |

## Logging

- `log/slog`, separate per binary (separate processes, separate journald/log-file
  streams under systemd/launchd anyway).
- Standard debug/info/warn/error. Debug includes raw HID report bytes (opt-in),
  replacing today's informal prints in `set_key_color.py`.
- **Hard rule**: bearer tokens are never logged at any level — log
  "token present/absent/invalid", never the value.

## Config

`~/.config/vialrgb-notify/config.yaml` (path TBD-but-conventional; not a placeholder
in the sense of being undecided-and-blocking, just not bikeshedded here). `connectord`
owns reading the whole file (naming rules, listener config, tokens) and hands `restd`
only what it needs over the socketpair at startup — keeping `restd` from needing
filesystem config access at all, and keeping config parsing in one place. Phase 4's
per-status YAML templates live under a `templates/` subdirectory of the same config
root, loaded by `restd` (template *interpretation* is `restd`'s job, unlike the base
daemon config).

## Testing

- `connectord`: unit tests for enumeration/identity-resolution/dedup logic against a
  fake HID backend — no physical board required for most of it (protocol correctness
  is already established by `set_key_color.py` against real hardware).
- `restd`: unit tests for HTTP handlers, auth middleware, and the color parser
  (including all three formats' happy paths and the `400` cases), against a fake
  internal-RPC client.
- Dispatcher/batching: unit tests asserting N concurrent same-device contiguous
  `SetKey`s collapse into one `SetKeys` RPC, different-device calls never merge, and a
  full queue returns `503` (inject a slow fake `connectord`).
- Manual/gated: real-hardware round-trip check, documented but not automated (CI has
  no physical keyboard attached).

## Package layout (Go, indicative)

```
vialrgb-notify/
  cmd/connectord/      main() — startup, spawn restd, supervise
  cmd/restd/            main() — listeners, dispatcher, handlers
  internal/hid/         enumeration, identity resolution, VialRGB protocol
  internal/ipc/         socketpair framing shared by both binaries
  internal/api/         HTTP handlers, color parsing, auth middleware
  internal/dispatcher/  restd's traffic-cop/batching logic
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
