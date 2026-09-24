# blinkenkeys: Phase 3 design — effects, templates, frame-buffer delivery

Date: 2026-09-24

## Scope

Phase 3 merges three items from the original `README.md` roadmap into one phase,
because they only make sense together:

- **Effects** (old roadmap item 4): Go-coded animation *primitives*, composed into
  named, multi-stage *effects* defined in YAML.
- **Abstraction templates** (old item 3): YAML files mapping an application's
  semantic states (`claude/idle`, `claude/working`) to a color or an effect.
- **Server-owned timers** (old item 6): a multi-stage effect with timed stages *is*
  a server-owned timer — the motivating `timer5min` example below is exactly old
  item 6's motivating example — so it needs no separate mechanism.

Phase 3 also includes three changes to already-shipped Phase 1+2 behavior, which
the above depends on:

- **Frame-buffer delivery model**: the color cache becomes the source of truth for
  desired state, written unconditionally; hardware delivery becomes best-effort and
  decoupled from the HTTP response.
- **Richer key and device addressing**: `R,C`, `led:N`, `idx:N`, a reserved name
  form, and numeric device ordinals.
- **Pre-declared optional devices**: a config option so writes to a device that
  hasn't been plugged in yet (laptop moved between desks) succeed and are delivered
  once it appears.

Phase 3 also has to wire `config.Load` into `main()`, which Phase 1+2 deferred.
The optional-device declarations and the effects/templates directories need it.

Specced here but **implemented in Phase 5**: the named-key registration/reclaim
model. Phase 3 only reserves its syntax (see "Key addressing").

Not in scope: multi-key effects (the old "radius explosion" — on hold, since
effects spanning several keys raise ownership/supersession questions nobody has
answered yet), hot reload of YAML (restart the daemon to pick up changes), listing
routes for effects/templates, the TCP listener (still deferred from Phase 1+2), and
per-client allocation (Phase 5).

## Background / constraints

- Existing architecture (Phase 1+2 design spec,
  `2026-09-21-blinkenkeys-phase1-2-design.md`): one process; one dispatcher
  goroutine owns every `hid.Controller`; `Registry` tracks name → slot
  (`Connected` / `Untethered`, untethered slots evicted after 24h); `Cache` holds
  last-known colors; `RunPeriodicRedraw` replays the cache every 5s and
  `RedrawReconnected` replays it immediately on reconnect.
- Today, `dispatchSetKeys` only calls `Cache.Update` **after a successful hardware
  write**, and `Registry.Get` only returns a controller for `Connected` slots, so a
  `PUT` to an unplugged-but-known device returns 404 and its update is lost. With
  effects in the picture, that's wrong: a 5-minute timer must keep advancing while
  the keyboard is unplugged, and show the *current* frame when it comes back.
- Colors are QMK-native HSV (0–255 each). All color strings in YAML and REST go
  through the existing `internal/color.Parse` (hex, `"H,S,V"`, CSS/X11 name).
- VialRGB packet ceiling: 9 LEDs per `SetKeys` report (existing `maxBatch`).

## Design

### 1. Frame-buffer delivery model (revises Phase 1+2 dispatcher behavior)

Mental model: `Cache` is a frame buffer; the keyboard is a canvas that the frame
buffer is copied onto whenever the canvas is available. Frames produced while the
canvas is away are never delivered. When it returns it gets the current frame,
not a replay.

**Write path (`SetKey`, renamed `Write(device, address, hsv)`), revised:**

1. Resolve the device (name or ordinal, see §4) to a registry slot. No slot —
   never enumerated since startup and not declared in `config.yaml` →
   `ErrDeviceNotFound` (→ 404), before any key-address resolution is attempted.
   This is now the only way `Write` fails at the device level; the pending list
   in step 2 exists only for config-declared devices.
2. Resolve the key address (see §3) against the slot's capabilities. If
   capabilities aren't known yet (a pre-declared device that has never connected,
   §5), store the write in that device's **pending** list on its registry slot
   (a later write to the same literal address replaces the earlier one and moves
   to the end), and return success. The check-capabilities-or-pend step is one
   atomic registry operation, so a write can't slip between "caps unknown" and
   "caps stored".
3. `Cache.Update(device, key)` — **synchronously, unconditionally**, from the
   caller's goroutine. `Cache` is already mutex-guarded; it doesn't need to go
   through the dispatcher queue.
4. Enqueue a **flush job** `(device, ledIndex)` non-blockingly, with no reply
   channel. If the queue is full, drop the job and log at debug. The cache already
   holds the truth, and the next periodic redraw delivers it.
5. Return success (HTTP 204, as today). The response never waits on or depends on
   the hardware write.

**Dispatcher goroutine, revised:**

- A flush job carries no color. At dispatch time the dispatcher reads the *current*
  cached color for `(device, ledIndex)`. This makes stale writes impossible: a
  flush enqueued by a periodic redraw a moment before an effect frame still sends
  the newest color. Duplicate flushes for the same key within one drained batch
  collapse into one.
- Batching (`groupForSend`) is unchanged in principle: same device, contiguous
  index, up to 9 per report, after de-duplication.
- If the slot isn't `Connected` at dispatch time, skip the group silently.
- A hardware write error is logged at warn and otherwise ignored. The device will
  typically show up as `Untethered` on the next 1s enumeration poll, and
  reconnect redraw catches it up.
- `ListDevices` and `GetCapabilities` stay synchronous request/reply jobs and can
  still return `ErrQueueFull` (→ 503).

**Redraw, revised:** `Redraw(device)` enqueues **one** redraw job, which the
dispatcher expands at dispatch time into flushes of every currently cached index
(one job per LED would overflow the 64-deep queue on any board with more than 64
LEDs). It no longer calls `SetKey`, so it never writes to the cache. Otherwise a snapshot
taken just before an effect frame would overwrite the newer frame in the cache.
`RunPeriodicRedraw` (5s, `Connected` slots only) and `RedrawReconnected` are
otherwise unchanged. Together they provide "delta on reconnect, full frame within
≤5s".

**Capabilities move onto the registry slot.** `GetCapabilities` queries the device
once per slot and stores the result on the slot. The stored result survives
`Untethered`, since a device's matrix doesn't change across a replug. Consequences:

- `GET /devices/{name}` for an `Untethered` device returns its cached capabilities
  (200) instead of failing.
- `R,C` / `idx:N` resolution works for `Untethered` devices.
- `api.CapabilitiesCache` is removed. Its job is now the slot's.
- For a slot that has never had capabilities (a pre-declared, never-connected
  device), `GET /devices/{name}` returns 503 ("capabilities not yet known").

**Capabilities fetch and pending resolution on connect.** On every poll, for every
name in `Registry.ConnectedWithoutCaps() ∪ Reconnected`, the poll loop calls
`Dispatcher.EnsureCapabilities(name)`. (`ConnectedWithoutCaps` rather than a
one-shot "newly added" list, so a device whose first capabilities fetch failed is
retried on the next poll instead of being stuck without capabilities.) That call
fetches and stores the capabilities if the slot lacks them, resolves every pending
entry into the cache (in arrival order, so the latest write per key wins), clears
the pending list, and redraws. Pending entries are applied to the cache while the
registry lock that stored the capabilities is still held, so a newer direct write
(which can only resolve after it observes the stored capabilities) always lands
after them. A pending entry that doesn't resolve (e.g. `5,9` on a 3×4 pad) is
dropped with a warn log naming the address.

The first time a device slot is created by enumeration, the daemon logs its name
with a copy-paste hint (`add "- id: <name>" with "optional: true" under devices:
to pre-declare it`), so setting up §5 doesn't require reading `GET /devices`.

**Effect on the error contract** (replaces the Phase 1+2 Error Handling table for
the routes below):

| Condition | Status |
|---|---|
| Malformed body, color, or address | 400 |
| Device name/ordinal with no registry slot (never enumerated, not pre-declared) | 404 |
| Key not on the device's matrix (capabilities known) | 404 |
| Unknown effect or template state | 404 |
| Name form in `{pos}` (reserved for Phase 5) | 501 |
| `PUT` to a known device, connected or not | 204 |
| `GET /devices/{name}` for a device whose capabilities were never fetched | 503 |
| Dispatcher queue full on `GET` routes | 503 |
| Missing/invalid bearer token when required | 401 / 403 (unchanged) |

"Named device currently disconnected → 503" is removed for `PUT`. The key status
for that route is 404 (never seen) vs 204 (seen, or declared).

### 2. Primitives, effects, templates

Three layers, each referencing only the one below:

```
template state (claude/idle) ──> effect (timer5min) ──> stages ──> primitive (alternate) / color / nested effect
```

**Primitives** are Go code in `internal/effects`, registered by name in a
`map[string]Primitive`. Adding a primitive later (e.g. a sine-wave `breathe_sine`)
means registering one more entry. No switch statements elsewhere change and no
schema changes. A primitive declares its settings (name, type) so the loader can
validate YAML against it, and it is a pure function
`(settings, elapsedInStage) → HSV`. Every setting is required: there are no
defaults, so an effect file states everything it does, and a missing setting
fails loading (and `--check-config`).

| Primitive | Settings (all required) | Behavior |
|---|---|---|
| `breathe` | `color`, `frequency_hz`, `duty_cycle` | Scales `color`'s V between 0 and its full V along a triangle wave. `duty_cycle` is the fraction of each period spent rising: 0.5 = symmetric triangle, 1.0 = rising sawtooth, 0.0 = falling sawtooth. H and S held. |
| `alternate` | `color_a`, `color_b`, `frequency_hz`, `duty_cycle` | Square wave: `color_a` for `duty_cycle` of each period, then `color_b`. At 1 Hz, 0.2 = 200 ms `color_a`, 800 ms `color_b`. |
| `blink` | `color`, `frequency_hz`, `duty_cycle` | Thin wrapper: `alternate` with `color_b` = black. |

Validation: `frequency_hz` > 0; `0 ≤ duty_cycle ≤ 1`; colors must pass
`color.Parse`.

A flat color is **not** a primitive, since it doesn't animate. It's its own stage
kind.

**Effects** live in `effects/<name>.yaml`; the effect name is the file name.
Schema:

```yaml
# effects/timer5min.yaml
stages:
  - duration: 3m
    color: "#00ff00"
  - duration: 1m
    primitive: alternate
    settings: { color_a: "#00ff00", color_b: "#ff0000", frequency_hz: 1, duty_cycle: 0.8 }
  - duration: 1m
    primitive: alternate
    settings: { color_a: "#00ff00", color_b: "#ff0000", frequency_hz: 1, duty_cycle: 0.2 }
final_state: "#ff0000"  # required on every effect that ends
```

- Each stage has exactly one of `color`, `primitive` (+ `settings`), or `effect`
  (a nested reference to another named effect). `settings` is only valid with
  `primitive`.
- `duration` is a Go duration string (`time.ParseDuration`: `1500ms`, `90s`, `3m`,
  `1h30m`), and must be > 0. It is required on every stage except the **last**.
  Omitting it there means "run until superseded", which is how an open-ended
  effect like "breathe blue while working" is written.
- `final_state` is required on an effect that ends, and optional on an open-ended
  one (it can never be reached; if given, it is still validated).
- A nested-effect stage without `duration` takes the referenced effect's total
  duration. If the referenced effect is open-ended, this is allowed only in the
  **last** stage, and makes the outer effect open-ended too (e.g. "flash red 3×,
  then breathe until superseded"). If the stage is shorter than the nested effect,
  the nested effect is cut off. If it's longer, the nested effect's `final_state`
  holds for the remainder.
- After the last timed stage ends, the key is set to `final_state` and the effect
  ends.
- **No effect parameters in Phase 3.** Every value in an effect file is literal;
  a variant (say, timer5min ending purple) is another effect file. Parameterized
  effects with per-request overrides are deferred to a much later phase.
- Stage timing is computed from elapsed wall-clock time since the effect started,
  not by counting frames, so it doesn't drift.

**Templates** live in `templates/<program>.yaml`; the namespace is the file name.
Each top-level key is a state, mapped to either a color or an effect:

```yaml
# templates/claude.yaml
working:
  effect: breathe_blue
idle:
  effect: timer5min
waiting:
  color: "#ffa500"
```

Addressed as `<program>/<state>`, e.g. `claude/idle`.

**Loading and validation** happen once at startup, from the config dir (§6).
Missing `effects/` or `templates/` directories are fine. Any of the following is
fatal: invalid file/effect/state names (`[a-z0-9_-]+`), unknown primitive,
missing/unknown/invalid primitive settings, bad colors, unknown effect references, effect-reference cycles (detected
by DFS over nested-effect stages), a missing `final_state` on an effect that
ends, unknown keys in any YAML mapping, or invalid `duration` values or
placement. Only `*.yaml` files are read; other files in those directories are
ignored. On a fatal error the daemon logs the file and reason and exits nonzero
rather than running with a partial set. Every effect is compiled at load time, so
load-time validation is complete and a request can never hit an invalid effect.

`blinkenkeysd --check-config` runs exactly this loading and validation (plus
`config.yaml`), prints `ok` or the error, and exits 0 or 1 without touching HID
or the socket. Since there's no hot reload, this is how to validate an edit before
restarting the service.

404s for an unknown effect or template state list the known names in the error
body (e.g. `unknown effect "timer5mn"; known: breathe_blue, timer5min`), standing
in for the deferred listing routes.

**Engine** (`internal/effects.Engine`): owns the map of running effects keyed by
target `(device, canonical address)`. It is the only writer path the HTTP layer
uses, so supersession is enforced in one place:

- `SetColor(target, hsv)` cancels any running effect on the target, then calls
  `Dispatcher.Write(device, address, hsv)` (the revised `SetKey` of §1).
- `Start(target, compiledEffect)` cancels any running effect on the target, then
  registers the new one with start time = now.
- **The new command always wins.** A superseded effect is dropped without writing
  its `final_state`, because the new command's own write replaces it immediately.
  `final_state` is written only when an effect runs to its natural end.
- **One tick goroutine at 5 fps (200 ms).** Each tick computes each running
  effect's color at its elapsed time. It calls `Write` only if the color differs
  from the last one emitted for that target, so solid stages don't generate
  traffic. Finished effects write `final_state` and are removed. The tick
  function takes `now` as a parameter so tests drive it without a real clock.
- Effects keep running while their device is `Untethered` or not yet seen. Their
  frames land in the cache or pending map, so a timer shows the correct stage the
  moment the keyboard returns.
- Canonical address: the API resolves any form to `led:N` before calling the
  engine, so `2,2`, `idx:10`, and `led:7` naming the same key supersede each
  other. If the device's capabilities aren't known yet, resolution blocks on
  the dispatcher until they arrive (same mechanism as `GetCapabilities`)
  rather than keying the target by the unresolved literal address form —
  closing what was originally shipped as an accepted, rare limitation (two
  independently-ticking effects fighting over one key when a command landed
  before first connect). A device that can never answer (no registry slot, or
  declared but never connected) still fails fast with `ErrDeviceNotFound` /
  `ErrCapsUnknown` rather than blocking forever.

### 3. Key addressing (`{pos}`)

Tried in this order:

| Form | Meaning |
|---|---|
| `R,C` (e.g. `2,3`) | Matrix row/column, resolved via capabilities (existing behavior). |
| `led:N` | Raw VialRGB LED index (firmware order — serpentine on the 12e4). Must be < LED count when known. |
| `idx:N` | Row-major reading-order index: capabilities' positions sorted by (row, col), then numbered 0.. — top-left to bottom-right. |
| anything else | A **key name**, to be registered or looked up (Phase 5). Phase 3 returns **501** with a message saying named keys aren't implemented yet. |

LEDs with no matrix key (VialRGB reports row and col `0xFF` for them, e.g.
underglow — `vialrgb.c`'s `VIALRGB_GET_LED_INFO`) never match an `R,C` address
and are excluded from `idx:` numbering; only `led:N` reaches them.

Parsing and resolution live in their own small package (`internal/keyaddr`), with
no dependency on the dispatcher, so the API, engine, and pending-resolution code
share one implementation.

**Named-key model (specced now, implemented in Phase 5).** Each key has a claim
state: `unclaimed`, `claimed-by-name(X)`, or `claimed-by-direct`.

- A write addressed by name `X` goes to the key already claimed by `X`. If there
  is none, `X` implicitly claims the next unclaimed key; an explicit registration
  call is also possible. Callers are stateless hooks, so no registration step is
  required.
- A direct (`R,C`/`led:`/`idx:`) write to a key claimed by name reclaims it as
  `claimed-by-direct`. The next write under that name gets a fresh unclaimed key
  rather than contesting the reclaimed one.
- Deferred to Phase 5: the unclaimed-pool ordering (pane/tab number → matching key
  when in range, otherwise an overflow pool starting at row 1), release (explicit
  release call and/or an idle timeout, e.g. 8h), and the registration route.

### 4. Device addressing (`/devices/{name}`)

`{name}` is either a device name or a **numeric ordinal** (`^[0-9]+$`; generated
names never look like this, since they are `uid-…` or `vvvv-pppp…`):

- The ordinal is the device's rank among **all registry slots** (`Connected`,
  `Untethered`, and pre-declared), sorted by name, computed fresh on each request.
  An out-of-range ordinal → 404.
- `GET /devices` now returns its list sorted by name (today the order is random
  map iteration), so list position = ordinal.
- Intended as a zero-configuration default for single-device setups, where the
  device is always `/devices/0`. Ordinals are **not stable** across topology
  changes on multi-device setups (a new device sorting before yours shifts it);
  use the name there.

### 5. Pre-declared optional devices

For the laptop case: the keyboard is only present at some desks, but hooks fire
everywhere.

```yaml
# config.yaml
devices:
  - id: uid-0123456789abcdef   # the name blinkenkeysd assigned it when seen (hid.BaseName)
    optional: true
```

- At startup, `Registry.Declare(id)` creates an `Untethered` slot named `id` with
  base identity `id` and no capabilities. Writes to it succeed (204) and go to its
  pending map (§1).
- When the physical device enumerates, the existing `Reconcile` matches it by base
  identity, with no new matching logic. It's reported as `Reconnected`, which
  triggers capabilities fetch, pending resolution, and redraw.
- Slots marked `optional` (declared ones, and any enumerated device whose id
  appears in `devices:` with `optional: true`) are **exempt from the 24h untethered
  eviction**, since the laptop may stay away from that desk for a weekend or
  longer.
- The id is the identity string, so the user runs the daemon once with the
  keyboard attached, reads the name from its first-seen log line (§1) or
  `GET /devices`, and puts it in config.
  VID/PID-tier ids work too, but inherit that tier's documented ambiguity.
- Each `id` must be non-empty, unique, and not all digits (an all-digit name
  would be ambiguous with a device ordinal, §4). The entry shape (`id` + flags)
  leaves room for later fields such as aliases. A `devices:` entry without
  `optional: true` has no effect in Phase 3.

### 6. Config wiring

> Revised post-implementation: the `$BLINKENKEYS_CONFIG_DIR` / `BLINKENKEYS_SOCKET`
> env vars described below were replaced with a `--config` CLI flag and
> `config.yaml`'s `listeners.socket.path` — see CHANGELOG.md's 2026-09-24
> "Post-implementation revision" entry for the reasoning.

- Config dir: `$BLINKENKEYS_CONFIG_DIR` if set, else
  `${XDG_CONFIG_HOME:-$HOME/.config}/blinkenkeys`. It contains `config.yaml`,
  `effects/`, and `templates/`.
- `config.yaml` becomes optional: if it's missing, defaults apply. The
  `listeners.socket.path` requirement is relaxed, and an empty value means the
  default `~/.local/state/blinkenkeys/api.sock`. Precedence for the socket path:
  `BLINKENKEYS_SOCKET` env > config > default, so current behavior is unchanged.
- New `devices:` section (§5). Existing `naming`/`listeners` fields keep their
  meaning; TCP binding stays deferred.
- `config.yaml` decoding becomes strict: an unknown key anywhere is a config error,
  so typos fail loudly. No existing user config can break, because Phase 1+2
  never wired `config.Load` into the daemon.
- `main()` loads config, then effects and templates, before enumeration. Any error
  is fatal at startup. `--check-config` (§2) stops right after this step.

### 7. REST surface

One write route, with the body choosing what to apply. Colors, effects, and states
all target keys the same way:

```
PUT /devices/{name|ordinal}/keys/{pos}
  {"color": "#ff0000"}
  {"effect": "timer5min"}
  {"state": "claude/idle"}
```

Exactly one of `color`, `effect`, `state` must be present, and no other field
(else 400).
Success is 204. `GET /devices` and `GET /devices/{name|ordinal}` are otherwise
unchanged, apart from the ordinal and untethered-capabilities behavior above.

Example, a Claude Code `Stop` hook on a single-pad setup:

```
curl --unix-socket ~/.local/state/blinkenkeys/api.sock \
  -X PUT -d '{"state":"claude/idle"}' http://localhost/devices/0/keys/0,0
```

### Package layout (indicative)

```
internal/keyaddr/     parse {pos} forms; resolve to LED index given positions
internal/effects/     primitive registry, effect/template model, YAML loader + validation,
                      compiled timeline (pure), Engine (tick loop, supersession)
internal/dispatcher/  frame-buffer Write, flush/redraw jobs, caps + pending list on
                      slots, Declare/optional slots, ConnectedWithoutCaps
internal/api/         body forms, ordinal + keyaddr resolution, engine as write path;
                      CapabilitiesCache removed
config/               optional config.yaml, devices: section, config-dir resolution
examples/config/      effects/timer5min.yaml, effects/breathe_blue.yaml,
                      templates/claude.yaml — loaded by a test so they stay valid
```

## Testing

- **Primitives and timelines** (pure functions, table-driven): `breathe` V at
  known phases for duty 0/0.5/1; `alternate` switch points at 1 Hz / 0.2 and 0.8;
  `blink` = `alternate` with black; stage boundaries for `timer5min` at 0,
  179.9 s, 180 s, 240 s, 300 s (→ `final_state`); open-ended last stage never
  finishes; nested cut-off and hold; open-ended nested effect as the last stage.
- **Loader**: every fatal case listed in §2 gets its own test (including a
  missing primitive setting); `final_state` optional only when open-ended;
  unknown-name errors list known names; the example files under
  `examples/config/` load cleanly.
- **Engine**: driven by explicit `Tick(now)` calls against a fake dispatcher:
  supersession (color cancels effect; effect cancels effect), no `final_state` on
  supersession, `final_state` on natural end, unchanged frames not re-sent.
- **keyaddr**: parse precedence, `idx:` ordering, out-of-range, name fallthrough,
  `0xFF` LEDs excluded.
- **Dispatcher**: `Write` on an `Untethered` slot updates the cache and returns
  nil without touching the controller; unknown device → `ErrDeviceNotFound`; flush
  reads the latest cached value (enqueue flush, update the cache, dispatch → the
  newer color is sent); duplicate flushes collapse; a full queue drops without
  error; `Redraw` doesn't mutate the cache and is one queue job regardless of LED
  count; capabilities retained across `Untethered`.
- **Registry**: `Declare` creates a claimable untethered slot; the real device
  claims it and is reported `Reconnected`; optional slots are never evicted;
  `ConnectedWithoutCaps` lists exactly the connected slots lacking capabilities.
- **Pending**: writes to a declared, never-connected device resolve on
  `EnsureCapabilities`, latest write per key wins, unresolvable entries are
  dropped.
- **Config**: strict decoding, `devices:` id validation, missing file → defaults,
  config-dir and socket-path precedence; `--check-config` exit codes.
- **API**: status table in §1 row by row; body exclusivity; ordinal resolution and
  out-of-range; 501 for names.
- **Manual/gated** (`docs/superpowers/manual-checks/phase3-effects.md`): run
  `claude/idle` on real hardware, check stage transitions; unplug mid-timer and
  replug after the stage changed → the current stage shows within ≤5 s; start the
  daemon with the pad unplugged and declared optional, `PUT`, then plug in → the
  color appears.

## Documentation changes in this phase

- `README.md` roadmap: item 3 becomes "effects + templates + server-owned timers";
  items 4 and 6 are marked as folded into Phase 3 (not renumbered, so "Phase 5 —
  per-client key allocation" keeps its number). Fixes the existing inconsistency
  where the README and the Phase 1+2 spec disagreed on which number effects had.
- `CHANGELOG.md`: add a Phase 3 entry pointing here for the superseded Phase 1+2
  error-table row and cache-on-success semantics.
- `docs/superpowers/manual-checks/phase3-effects.md` (the gated checks above).

## Explicitly deferred

Multi-key effects / explosion; named-key allocation implementation (Phase 5 —
syntax reserved, returns 501); YAML hot reload / SIGHUP; `GET /effects` and
`GET /templates` listing; TCP listener binding; sine-wave `breathe` and other
future primitives (the registry makes these additive).
