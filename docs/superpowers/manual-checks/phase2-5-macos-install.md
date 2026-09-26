# Phase 2.5 macOS install/uninstall check

Manual, mutates real system state (`~/Library/LaunchAgents/`, `launchd` load
state) — not run in CI. Run after any change to `packaging/macos/`.

Prerequisites: `make build` has produced `bin/blinkenkeysd`. No `blinkenkeysd`
instance already running outside launchd (stop any manually-started one
first, or `install.sh`'s `kickstart`/`bootstrap` step will conflict with it
holding the HID device).

1. Confirm a clean starting state:
   ```bash
   launchctl print "gui/$(id -u)/com.seefood.blinkenkeysd" >/dev/null 2>&1 && echo "loaded" || echo "not loaded"
   ls "${XDG_BIN_HOME:-$HOME/.local/bin}"/blinkenkeysd 2>&1
   ls "$HOME/Library/LaunchAgents/com.seefood.blinkenkeysd.plist" 2>&1
   ```
   If any of these already exist from a prior run, run
   `packaging/macos/uninstall.sh` first and re-confirm they're gone.

2. **Fresh install:**
   ```bash
   packaging/macos/install.sh
   ```
   Confirm: no `sudo` prompt (nothing here needs root), prints both artifacts
   as "installed", ends with `launchctl print` output showing `state =
   running` (or `xpcproxy` while still starting) and a `pid`.

3. **Idempotent re-run:**
   ```bash
   packaging/macos/install.sh
   ```
   Confirm: both artifacts print "already up to date", the `pid` in the final
   `launchctl print` output is the *same* as step 2 (confirms no reload/kick
   happened for a no-op run).

4. **`--force` re-install while running:**
   ```bash
   packaging/macos/install.sh --force
   ```
   Confirm: both artifacts print as (re)installed, the `pid` in the final
   `launchctl print` output is *different* from step 3 (confirms the
   bootout+bootstrap reload path actually happened, not just a re-copy on
   disk).

5. **Functional check:** with the agent running from step 4, confirm the
   daemon actually works end-to-end:
   ```bash
   curl --unix-socket ~/.local/state/blinkenkeys/api.sock http://localhost/devices
   ```
   Confirm it lists your connected board(s) — same check as
   `phase1-2-hardware-roundtrip.md` step 3 and
   `phase2-5-linux-install.md` step 7.

6. **Uninstall:**
   ```bash
   packaging/macos/uninstall.sh
   ```
   Confirm: `launchctl print "gui/$(id -u)/com.seefood.blinkenkeysd"` now
   exits nonzero (not loaded), and both
   `${XDG_BIN_HOME:-$HOME/.local/bin}/blinkenkeysd` and
   `~/Library/LaunchAgents/com.seefood.blinkenkeysd.plist` are gone.

7. **Idempotent uninstall re-run:**
   ```bash
   packaging/macos/uninstall.sh
   ```
   Confirm: exits 0, no errors, all three summary lines print "already
   absent"/"was not loaded".

8. **User data survives uninstall:** confirm
   `${XDG_CONFIG_HOME:-~/.config}/blinkenkeys/` (if you'd copied
   `examples/config/` there) and `~/.local/state/blinkenkeys/` are both still
   present after step 6 — `uninstall.sh` must not have touched them.

## Verified

Run end-to-end on real macOS hardware (Darwin 25.6.0, arm64) against the
`cxt_studio/12e4` board, 2026-09-26: all eight steps passed as described
above, including the functional check returning the connected device.
