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
