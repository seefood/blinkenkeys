# Phase 2.5 Linux install/uninstall check

Manual, mutates real system state (`/etc/udev/rules.d/`, `systemd --user`
state) — not run in CI. Run after any change to `packaging/linux/`.

Prerequisites: `make build` has produced `bin/blinkenkeysd`. No `blinkenkeysd`
instance already running outside systemd (stop any manually-started one
first, or `install.sh`'s `restart` step will conflict with it holding the
HID device).

1. Confirm a clean starting state:
   ```bash
   systemctl --user status blinkenkeysd.service 2>&1 | head -1
   ls /etc/udev/rules.d/99-blinkenkeys.rules 2>&1
   ls ~/.local/bin/blinkenkeysd 2>&1
   ```
   If any of these already exist from a prior run, run
   `packaging/linux/uninstall.sh` first and re-confirm they're gone.

2. **Fresh install:**
   ```bash
   packaging/linux/install.sh
   ```
   Confirm: prompts for `sudo` password once (for the udev rule), prints all
   three artifacts as "installed", ends with
   `systemctl --user status blinkenkeysd.service` showing `active (running)`.

3. **Existing `99-vial.rules` untouched:**
   ```bash
   diff <(git -C ~/dotfiles-or-wherever show HEAD:path/to/99-vial.rules 2>/dev/null || cat /etc/udev/rules.d/99-vial.rules) /etc/udev/rules.d/99-vial.rules
   ```
   (Adjust to however you can independently confirm the file's prior
   contents — even just `ls -la --time-style=full-iso
   /etc/udev/rules.d/99-vial.rules` and confirming the mtime didn't change
   is sufficient.) Confirm it's untouched by step 2.

4. **Idempotent re-run:**
   ```bash
   packaging/linux/install.sh
   ```
   Confirm: no `sudo` prompt this time, all three artifacts print "already
   up to date", `systemctl --user status` still shows the *same* process
   (compare `Main PID` before/after — it must NOT have restarted).

5. **`--force` re-install while running:**
   ```bash
   packaging/linux/install.sh --force
   ```
   Confirm: all three artifacts print as (re)installed, `Main PID` in the
   final status output is *different* from step 4 (confirms the restart
   actually happened, not just a re-copy on disk).

6. **Functional check:** with the service running from step 5, confirm the
   daemon actually works end-to-end:
   ```bash
   curl --unix-socket ~/.local/state/blinkenkeys/api.sock http://localhost/devices
   ```
   Confirm it lists your connected board(s) — same check as
   `phase1-2-hardware-roundtrip.md` step 3.

7. **Uninstall:**
   ```bash
   packaging/linux/uninstall.sh
   ```
   Confirm: `systemctl --user status blinkenkeysd.service` now reports
   inactive/not-found, `~/.local/bin/blinkenkeysd`,
   `~/.config/systemd/user/blinkenkeysd.service`, and
   `/etc/udev/rules.d/99-blinkenkeys.rules` are all gone. Confirm
   `/etc/udev/rules.d/99-vial.rules` is still present and unchanged.

8. **Idempotent uninstall re-run:**
   ```bash
   packaging/linux/uninstall.sh
   ```
   Confirm: exits 0, no errors, no `sudo` prompt (nothing left to remove),
   all four summary lines print "already absent"/"was not loaded".

9. **User data survives uninstall:** confirm
   `${XDG_CONFIG_HOME:-~/.config}/blinkenkeys/` (if you'd copied
   `examples/config/` there) and `~/.local/state/blinkenkeys/` are both
   still present after step 7 — `uninstall.sh` must not have touched them.
