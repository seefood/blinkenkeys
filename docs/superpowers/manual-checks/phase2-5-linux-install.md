# Phase 2.5 Linux install/uninstall check

Manual, mutates real system state (`/etc/udev/rules.d/`, `systemd --user`
state) — not run in CI. Run after any change to `packaging/linux/`.

Prerequisites: `make build` has produced `bin/blinkenkeysd`. No `blinkenkeysd`
instance already running outside systemd (stop any manually-started one
first, or `install.sh`'s `restart` step will conflict with it holding the
HID device).

0. **Record a baseline for the pre-existing `99-vial.rules`,** so step 3 below
   has something to compare against:
   ```bash
   sha256sum /etc/udev/rules.d/99-vial.rules
   stat -c '%Y' /etc/udev/rules.d/99-vial.rules
   ```
   Keep this output; steps 3, 5, and 7 re-run it and diff by eye.

1. Confirm a clean starting state:
   ```bash
   systemctl --user status blinkenkeysd.service 2>&1 | head -1
   ls "${XDG_BIN_HOME:-$HOME/.local/bin}"/blinkenkeysd 2>&1
   ls /etc/udev/rules.d/70-blinkenkeys.rules 2>&1
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

   `~/.config/systemd/user/` not existing yet on a genuinely fresh machine
   (Review Focus: missing parent directories) is covered by code inspection
   only (`install.sh`'s `mkdir -p "$BIN_DIR" "$UNIT_DIR"`), not exercised
   live here — this checklist runs on a dev machine that already has that
   directory. If you have access to a container or fresh user account with
   no `~/.config/systemd/` at all, running this step there once is a
   stronger check.

3. **Existing `99-vial.rules` untouched:**
   ```bash
   sha256sum /etc/udev/rules.d/99-vial.rules
   stat -c '%Y' /etc/udev/rules.d/99-vial.rules
   ```
   Confirm both match step 0's baseline exactly (same hash, same mtime).

4. **Access actually comes from the new rule, not from `99-vial.rules`:**
   for every `hidraw` node the new rule matches, confirm it carries a real
   uaccess ACL entry, not just the `uaccess` tag in udev's internal db:
   ```bash
   for d in /dev/hidraw*; do
     udevadm info -a -n "$d" | grep -q 'ATTRS{serial}=="vial:' \
       && echo "$d:" && getfacl -p "$d" | grep "user:$USER:rw" \
       && echo "  ^ found (ok)" || true
   done
   ```
   Expect a `user:$USER:rw-` line printed for each matching node. This is
   the check that actually exercises `70-blinkenkeys.rules`'s `uaccess`
   grant in isolation — `99-vial.rules`'s `GROUP=` grant would make the
   daemon work even if the new rule's tag were silently ignored (e.g. by a
   rule filename ordering bug), so the functional check in step 6 alone
   cannot catch that class of failure.

5. **Idempotent re-run:**
   ```bash
   packaging/linux/install.sh
   ```
   Confirm: no `sudo` prompt this time, all three artifacts print "already
   up to date", `systemctl --user status` still shows the *same* process
   (compare `Main PID` before/after — it must NOT have restarted). Re-check
   `99-vial.rules`'s hash/mtime against step 0 again.

6. **`--force` re-install while running:**
   ```bash
   packaging/linux/install.sh --force
   ```
   Confirm: all three artifacts print as (re)installed, `Main PID` in the
   final status output is *different* from step 5 (confirms the restart
   actually happened, not just a re-copy on disk). Re-check `99-vial.rules`
   against step 0 again.

7. **Functional check:** with the service running from step 6, confirm the
   daemon actually works end-to-end:
   ```bash
   curl --unix-socket ~/.local/state/blinkenkeys/api.sock http://localhost/devices
   ```
   Confirm it lists your connected board(s) — same check as
   `phase1-2-hardware-roundtrip.md` step 3.

8. **Uninstall:**
   ```bash
   packaging/linux/uninstall.sh
   ```
   Confirm: `systemctl --user status blinkenkeysd.service` now reports
   inactive/not-found, `${XDG_BIN_HOME:-$HOME/.local/bin}/blinkenkeysd`,
   `~/.config/systemd/user/blinkenkeysd.service`, and
   `/etc/udev/rules.d/70-blinkenkeys.rules` are all gone. Confirm
   `/etc/udev/rules.d/99-vial.rules` is still present, and its hash/mtime
   still match step 0.

9. **Idempotent uninstall re-run:**
   ```bash
   packaging/linux/uninstall.sh
   ```
   Confirm: exits 0, no errors, no `sudo` prompt (nothing left to remove),
   all four summary lines print "already absent"/"was not loaded".

10. **User data survives uninstall:** confirm
    `${XDG_CONFIG_HOME:-~/.config}/blinkenkeys/` (if you'd copied
    `examples/config/` there) and `~/.local/state/blinkenkeys/` are both
    still present after step 8 — `uninstall.sh` must not have touched them.
