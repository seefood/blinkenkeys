# Changelog

Design and architecture revisions, newest last. Each entry names the spec it
revises.

## Phase 1+2 design (`docs/superpowers/specs/2026-09-21-blinkenkeys-phase1-2-design.md`)

- **2026-09-23**: Removed the `connectord`/`restd` privilege-separation split after
  disproving its premise. The split assumed macOS's Input Monitoring TCC grant gated
  raw HID access to VialRGB's vendor-defined usage page, requiring a stable-identity,
  always-unprivileged component to hold that access. Investigation (Vial's own
  `vial-gui` source, `TCC.db` inspection, and a real hardware test) showed this access
  was never gated in the first place. A parallel CDC-ACM ("virtual serial") POC, built
  to route around the believed TCC gate, was also reverted from the `qmk_vial` firmware
  once raw HID was confirmed to work without it — see `README.md`'s "Known
  limitations" section and git history on `personal/vialrgb-direct/001-enable` for
  that firmware experiment. Added the "State persistence & refresh" section, motivated
  by hardware testing showing `g_direct_mode_colors` doesn't survive a reset and
  VialRGB exposes no way to read it back.
- **2026-09-23**: Refined "State persistence & refresh" with untethered rewire and 24h
  cache eviction: a disconnected device's name/cache now survives a replug (rewire)
  instead of being freed, without letting a second identity-colliding device steal an
  already-`connected` slot, and without retaining a genuinely-gone device's cache
  forever.

## Phase 3 design (`docs/superpowers/specs/2026-09-24-blinkenkeys-phase3-effects-templates-design.md`)

- **2026-09-24**: Supersedes two Phase 1+2 behaviors. (1) The color cache is now a
  frame buffer written unconditionally on every accepted write, with hardware
  delivery best-effort and decoupled from the HTTP response — Phase 1+2 updated
  the cache only after a successful hardware write. (2) Error table: "named
  device currently disconnected → 503" is removed for `PUT`; a known (seen or
  config-declared) device returns 204 whether connected or not, and only a device
  with no registry slot returns 404. Capabilities now live on the registry slot
  and survive disconnects, so `GET /devices/{name}` answers for untethered
  devices too (503 only if capabilities were never fetched).
- **2026-09-24**: Planning-review revisions to the Phase 3 spec itself: `duration`
  Go duration strings instead of `duration_ms`; `final_state` optional on
  open-ended effects; an open-ended nested effect is allowed as the last stage;
  no effect parameters (deferred to a much later phase: every value in an effect
  file is literal); primitive settings are all required, under the stage key
  `settings`; strict `config.yaml` decoding; `0xFF` LEDs excluded from
  `R,C`/`idx:`; `ConnectedWithoutCaps` drives the capabilities retry (`Added` only
  feeds the first-seen log hint); single-job redraw; `--check-config`; unknown
  effect/state 404s list the known names; `duration: 3m` strings.
  Accepted known gap: an effect started on a pre-declared device before it first
  connects keeps its literal-address target, so a later command on the same key in another form doesn't supersede it.
- **2026-09-24**: Post-implementation revision, made during Phase 3 integration
  verification: dropped the `BLINKENKEYS_CONFIG_DIR` and `BLINKENKEYS_SOCKET`
  env vars in favor of a `--config` CLI flag and `config.yaml`'s
  `listeners.socket.path`, respectively — config now comes from a file (or an
  explicit flag naming that file's directory) rather than ambient environment
  state. `config.Dir` keeps `$XDG_CONFIG_HOME` as the standard fallback base for
  the *default* location; it no longer accepts an override env var. `--config`
  also got a `-c` shorthand, per GNU CLI convention (single dash reserved for
  single-letter names; `--check-config` has no shorthand).
- **2026-09-24**: Accepted known gap, found during hardware verification: a
  running `breathe` effect's peak (and trough) brightness usually misses its
  target V by an amount that scales with the effect's frequency and duty
  cycle, not a fixed percentage — worst case is about
  `0.1s * frequency_hz / min(duty_cycle, 1-duty_cycle)` of full V, which is
  ~10% for the shipped `breathe_blue.yaml` example (0.5 Hz, duty 0.5) but
  ~20% at 1 Hz duty 0.5; effects above roughly 2.5 Hz can't be represented
  meaningfully on a 5fps tick at all (Nyquist). `breatheFrame`'s math is
  correct and reaches exactly full V at the true mathematical peak (see its
  unit tests); the shortfall comes from `Engine.Tick` sampling on a global
  tick grid whose phase is independent of each effect's start time, so the
  exact peak sample is rarely landed on. Not fixed in Phase 3: a candidate
  partial fix (align each effect's start time to the previous tick instead
  of the command's `time.Now()`) looks like it would avoid the visible phase
  jump first assumed here, but that's unverified since the change itself
  hasn't been built — and even if it holds, the fix only lands the peak
  exactly when `duty_cycle / frequency_hz` is itself a multiple of the tick
  interval (true for the shipped example, not for e.g. 1 Hz duty 0.5), so
  at best it trades one known gap for a narrower one rather than closing
  it. Left open for a later decision on whether that trade is worth taking.
- **Future work (not scheduled)**: `breathe` currently scales V from 0 to the
  given color's full V; raised during the same hardware session as a possible
  follow-up is a `min`/`max` V pair (or fraction) so it breathes between two
  brightness *levels* of one hue, generalizing today's behavior (`min: 0`
  reproduces it exactly). A true two-color crossfade (blending H and S too,
  not just V) was considered and set aside as a separate, larger primitive —
  `alternate` already covers "two colors" as a square wave. Deferred to a
  later phase's own brainstorm rather than folded into Phase 3.

## Phase 5: named-key claim model (specced inline in the Phase 3 doc's "Named-key
model" section)

- **2026-09-24**: Implemented as specced, with one deliberate simplification:
  the unclaimed-pool order is a flat ascending row/col scan (row 0 excluded,
  reserved for direct addressing) rather than the spec's "pane/tab number →
  matching key when in range, otherwise an overflow pool" scheme — terminal
  tab/pane correlation is still unsolved (see `README.md` roadmap item 5), so
  there was nothing for that ordering to key off of yet. Claims release via
  `DELETE /devices/{name}/keys/{key-name}` or an idle sweep (default 8h,
  `claims.idle_timeout` in `config.yaml`).
- **2026-09-24**: Real hooks wired against this model on real hardware surfaced
  that `examples/config/templates/claude.yaml`'s `waiting` state (previously a
  static `#ffa500`) reads better folded into `timer5min` like `idle` — from a
  prompt-cache-freshness standpoint, "blocked on a permission prompt" and
  "turn ended, waiting for the next prompt" are the same case: no API call is
  in flight, so the cache clock is ticking either way. `working` moved from
  `breathe_blue` to a new `breathe_orange.yaml` (same `breathe` primitive,
  `color: orange`); `breathe_blue.yaml` is left in place, now unreferenced, as
  a `breathe`-primitive example.
