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
  feeds the first-seen log hint); single-job redraw; `-check-config`; unknown
  device/effect/state 404s list the known names; `duration: 3m` strings.
  Accepted known gap: an effect started on a pre-declared device before it first
  connects keeps its literal-address target, so a later command on the same key in another form doesn't supersede it.
