# blinkenkeys: Phase 2.5 design — service installers

Date: 2026-09-24

## Scope

This spec covers packaging `blinkenkeysd` (the single binary produced by Phases
1+2, already hardware-validated — see
`2026-09-21-blinkenkeys-phase1-2-design.md`) as a per-user background service on
Linux and macOS: a udev rule (Linux only) for unprivileged HID access, a
service-manager unit (`systemd --user` on Linux, a `launchd` LaunchAgent on
macOS), and an idempotent install script for each platform.

Both platforms are fully specced here, down to literal file contents. **Only
the Linux installer is implemented in this pass** — the macOS design is
complete enough to implement from directly, but the user will write the
actual `packaging/macos/install.sh` and test it later on their own laptop,
since this session has no macOS machine to verify against. This is not part
of the original 6-phase product roadmap (`README.md` has no packaging/install
phase); it's follow-on work the user asked for after Phases 1+2 landed.

Not in scope: `config.Load` wiring into `main()` (still deferred, tracked
separately — see the Phase 1+2 plan's "Explicitly deferred" section), and any
Windows service story. (A Linux `uninstall.sh` is in scope — added after
initial review.)

## Background / constraints

- `blinkenkeysd` is a single Go binary (`bin/blinkenkeysd`) that blocks in the
  foreground on `http.Serve` — it does not daemonize/fork itself, so it needs a
  service manager to run it in the background and restart it on failure, not a
  traditional SysV-style double-fork daemon.
- It listens by default on a Unix socket at
  `~/.local/state/blinkenkeys/api.sock` (`BLINKENKEYS_SOCKET` env var
  override); no TCP listener is wired up by default.
- Linux HID access: `/dev/hidraw*` is root-owned by default. The design spec's
  stated primary mechanism is a udev rule granting the logged-in user access
  (`TAG+="uaccess"`, the systemd-logind seat-based ACL grant), root is a
  fallback only. The user already has a hand-edited
  `/etc/udev/rules.d/99-vial.rules` (installed per Vial's own upstream
  instructions, predating this project) that must not be overwritten or
  clobbered by this installer.
- A live investigation during this session (raw `hidraw` sysfs/report-descriptor
  reads, `lsusb -v`, and a throwaway Go enumeration probe) established a
  concrete fact this design relies on: the udev-visible `ATTRS{serial}` string
  on a Vial-firmware device's raw-HID interface is a **firmware-build-time
  fingerprint** in the form `vial:<hash>` — shared by every physical board
  built from the same Vial keyboard-definition/config (confirmed: the user's
  existing rule, written for a different physical board over 18 months ago,
  already matches a newly-flashed second board because both share a firmware
  build). This is distinct from `VIAL_KEYBOARD_UID` (the actual per-physical-
  device identity `blinkenkeysd` itself queries live over the HID protocol for
  its `Registry`/`BaseName` naming — see the Phase 1+2 spec). The udev layer
  only needs to grant access to the interface, not distinguish devices, so
  matching on the `vial:*` prefix generically (any Vial-firmware device) is
  both correct and simpler than trying to scope by VID/PID or an exact hash.
- macOS needs no udev analog at all: per the Phase 1+2 spec's corrected
  finding, raw HID access to a vendor-defined usage page (`0xFF60`/`0x61`)
  requires no Input Monitoring TCC grant. The macOS installer only needs to
  place the binary and a LaunchAgent plist.
- Per CLAUDE.md: macOS runs as a normal user process (LaunchAgent, never a
  LaunchDaemon/root); Linux prefers the udev rule over running as root, with
  root as a fallback-only path that isn't addressed by this installer (no
  udev-rule-install-failed fallback flow is built here — if `sudo` fails the
  script just errors).

## Design

### Component layout

```
packaging/
  linux/
    blinkenkeysd.service   # systemd --user unit template
    70-blinkenkeys.rules   # udev rule template
    install.sh             # Linux installer (implemented this pass)
    uninstall.sh           # Linux uninstaller (implemented this pass)
  macos/
    com.seefood.blinkenkeysd.plist   # LaunchAgent template
    install.sh                        # macOS installer (design only — not implemented this pass)
docs/superpowers/manual-checks/
  phase2-5-linux-install.md   # manual verification checklist, implemented this pass
```

Unit/rule/plist content ships as separate, reviewable template files rather
than heredocs embedded in the install scripts, so the actual artifacts placed
on a user's system are diffable in the repo and in `git log`.

### Linux: udev rule

`packaging/linux/70-blinkenkeys.rules`:

```
KERNEL=="hidraw*", SUBSYSTEM=="hidraw", ATTRS{serial}=="vial:*", MODE="0660", TAG+="uaccess"
```

Installed to `/etc/udev/rules.d/70-blinkenkeys.rules` — a name distinct from
the user's existing `99-vial.rules`, so the installer only ever writes its own
file and never touches the pre-existing one. `TAG+="uaccess"` matches the
design spec's stated primary mechanism (systemd-logind seat ACL); this project
doesn't add a `GROUP=`-based fallback.

The `70-` prefix is load-bearing, not cosmetic: systemd ships
`73-seat-late.rules`, whose `TAG=="uaccess", RUN{builtin}+="uaccess"` line is
what actually applies the ACL, and udev evaluates `/etc/udev/rules.d/` in
lexical filename order. A rule numbered `99-` adds the `uaccess` tag *after*
`73-seat-late.rules` already ran, so the builtin is never queued and no ACL is
granted — confirmed on the dev machine (final review, 2026-09-24): the earlier
draft's `99-blinkenkeys.rules` left the matching `hidraw` nodes with no
`user:<name>:rw-` ACL entry at all. The dev machine's access came entirely
from the pre-existing `99-vial.rules`'s `GROUP="1000"` line, which hid the
gap. `70-` sits alongside systemd's own `70-uaccess.rules`, safely before
`73-seat-late.rules`.

### Linux: systemd `--user` unit

`packaging/linux/blinkenkeysd.service`:

```ini
[Unit]
Description=blinkenkeysd — VialRGB keyboard-lighting daemon

[Service]
Type=simple
ExecStart=__BLINKENKEYSD_BIN__
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
```

`__BLINKENKEYSD_BIN__` is a literal placeholder token; `install.sh` substitutes
it with the resolved binary path before writing the unit out, so the checked-
in template never hardcodes a path. `WantedBy=default.target` + `systemctl
--user enable --now` starts the daemon at login, not on a device-hotplug
trigger — `blinkenkeysd`'s own hotplug polling (Phase 1+2) already handles
devices attaching after the daemon is already running, so a udev-triggered
unit would add complexity without adding capability.

### Linux: binary install location

`${XDG_BIN_HOME:-$HOME/.local/bin}/blinkenkeysd`. `XDG_BIN_HOME` isn't part of
the official XDG Base Directory spec but is a common convention; `~/.local/bin`
is the fallback when it's unset.

### Linux: `install.sh`

Single script, run as the normal user; only the individual lines that need
root privilege shell out to `sudo` themselves (not one coarse `sudo` wrapper
block) — the user types their password once and `sudo` caches it for the rest
of the script's short runtime. Idempotent by default: each of the three
artifacts (binary, systemd unit, udev rule) is compared against what's already
installed and only rewritten if different or `--force` is passed.

Steps:

1. Resolve `BIN_DIR=${XDG_BIN_HOME:-$HOME/.local/bin}`, `mkdir -p` it.
2. Require `bin/blinkenkeysd` already exists in the repo checkout (built via
   `make build`); error out with a clear message pointing at `make build` if
   missing, rather than duplicating build logic in two places.
3. Copy `bin/blinkenkeysd` → `$BIN_DIR/blinkenkeysd`. Skip (no-op, no `sudo`
   involved here — `$BIN_DIR` is user-owned) if `cmp -s` shows the destination
   is already byte-identical, unless `--force`. Track whether it changed.
4. Render `packaging/linux/blinkenkeysd.service` (substitute
   `__BLINKENKEYSD_BIN__` → `$BIN_DIR/blinkenkeysd`) and write to
   `${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/blinkenkeysd.service`. Skip
   if identical unless `--force`. Track whether it changed.
5. Compare `packaging/linux/70-blinkenkeys.rules` against
   `/etc/udev/rules.d/70-blinkenkeys.rules`; if missing or different (or
   `--force`), `sudo install -m 0644` it into place, then
   `sudo udevadm control --reload-rules && sudo udevadm trigger`. Skip both
   `sudo` calls entirely if the file's already correct.
6. `systemctl --user daemon-reload` if the unit is new or changed.
7. `systemctl --user enable --now blinkenkeysd.service`. If the service was
   already running and step 3 or step 4 changed something,
   `systemctl --user restart blinkenkeysd.service` explicitly afterward —
   `enable --now` alone won't restart an already-running unit, so a rebuild-
   and-reinstall without this would silently keep running the old binary.
8. Print a short summary (`systemctl --user status blinkenkeysd.service
   --no-pager`) and which of the three artifacts were installed vs. already
   up to date vs. force-reinstalled.

`--force`: reinstalls/overwrites all three artifacts unconditionally,
regardless of whether they differ.

### Linux: `uninstall.sh`

`packaging/linux/uninstall.sh`. Reverses each `install.sh` step, in roughly
reverse order, and is itself idempotent — safe to run on a partial or already-
removed install (each step checks existence first and no-ops if the artifact
isn't there, rather than erroring).

Steps:

1. If `systemctl --user is-enabled blinkenkeysd.service` (or `is-active`)
   succeeds, `systemctl --user disable --now blinkenkeysd.service`. Skip if
   the unit isn't loaded at all.
2. Remove `${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/blinkenkeysd.service`
   if present, then `systemctl --user daemon-reload`.
3. If `/etc/udev/rules.d/70-blinkenkeys.rules` exists, `sudo rm` it, then
   `sudo udevadm control --reload-rules && sudo udevadm trigger`. Never
   touches `99-vial.rules`.
4. Remove `${XDG_BIN_HOME:-$HOME/.local/bin}/blinkenkeysd` if present.
5. Print a summary of what was removed vs. already absent.

Doesn't remove `${XDG_CONFIG_HOME:-$HOME/.config}/blinkenkeys` (user config/
effects/templates) or `~/.local/state/blinkenkeys` (socket dir, cache) —
those are user data, not installer-owned artifacts, so uninstalling the
service shouldn't delete them.

### macOS: LaunchAgent plist

No udev analog needed — no TCC grant gates this usage page (see Background),
so there's no access-control artifact to install at all, only the binary and
a LaunchAgent.

`packaging/macos/com.seefood.blinkenkeysd.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.seefood.blinkenkeysd</string>
    <key>ProgramArguments</key>
    <array>
        <string>__BLINKENKEYSD_BIN__</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>StandardOutPath</key>
    <string>__LOG_DIR__/blinkenkeysd.log</string>
    <key>StandardErrorPath</key>
    <string>__LOG_DIR__/blinkenkeysd.log</string>
</dict>
</plist>
```

- `KeepAlive.SuccessfulExit = false` is launchd's equivalent of
  `Restart=on-failure`: relaunch on a crash/nonzero exit, not on a clean exit.
- `__BLINKENKEYSD_BIN__` and `__LOG_DIR__` are literal placeholder tokens,
  substituted by `install.sh` the same way the systemd template's
  `__BLINKENKEYSD_BIN__` is — `LOG_DIR` resolves to the same
  `~/.local/state/blinkenkeys` directory `blinkenkeysd` already uses for its
  socket. Explicit log paths are necessary here (unlike the Linux unit, which
  relies on `journalctl --user` auto-capturing stdout/stderr): a LaunchAgent
  with no `Standard{Out,Error}Path` set discards that output, which would be a
  silent regression in debuggability versus the Linux side.
- Installed to `~/Library/LaunchAgents/com.seefood.blinkenkeysd.plist` — a
  fixed, OS-mandated location (no XDG equivalent applies here), and a
  LaunchAgent, never a LaunchDaemon (CLAUDE.md hard constraint).

### macOS: `install.sh`

Same shape as the Linux script — single script, idempotent unless `--force`,
same `$XDG_BIN_HOME`/`~/.local/bin` binary-location convention — but no `sudo`
lines at all, since nothing here needs root, and `launchctl` in place of
`systemctl --user`.

Steps:

1. Resolve `BIN_DIR=${XDG_BIN_HOME:-$HOME/.local/bin}`, `mkdir -p` it.
2. Require `bin/blinkenkeysd` already exists (built via `make build`); error
   out if missing, same as Linux.
3. Copy `bin/blinkenkeysd` → `$BIN_DIR/blinkenkeysd`; skip if `cmp -s` shows
   it's already identical, unless `--force`. Track whether it changed.
4. `mkdir -p ~/.local/state/blinkenkeys` (the log directory) defensively.
5. Render `packaging/macos/com.seefood.blinkenkeysd.plist` (substitute
   `__BLINKENKEYSD_BIN__` and `__LOG_DIR__`) and write to
   `~/Library/LaunchAgents/com.seefood.blinkenkeysd.plist`. Skip if identical
   unless `--force`. Track whether it changed.
6. Check whether the agent is currently loaded:
   `launchctl print gui/$(id -u)/com.seefood.blinkenkeysd >/dev/null 2>&1`
   (exit 0 = loaded, nonzero = not loaded).
7. Reconcile load state against what changed:
   - Not loaded: `launchctl bootstrap gui/$(id -u)
     ~/Library/LaunchAgents/com.seefood.blinkenkeysd.plist`.
   - Loaded and the plist changed (or `--force`): `launchctl bootout
     gui/$(id -u)/com.seefood.blinkenkeysd` then `bootstrap` again — a
     changed plist requires a full reload, `kickstart` alone won't pick up
     new plist content.
   - Loaded, only the binary changed, plist unchanged: `launchctl kickstart -k
     gui/$(id -u)/com.seefood.blinkenkeysd` (restart the process; `-k` kills
     the running instance first) — `ProgramArguments`' path didn't change, so
     a full reload isn't needed.
   - Loaded, nothing changed, no `--force`: no-op.
8. Print a summary: `launchctl print gui/$(id -u)/com.seefood.blinkenkeysd`
   plus which artifacts were installed/unchanged/force-reinstalled.

`--force`: reinstalls the binary and plist unconditionally and always does the
bootout+bootstrap reload path, mirroring the Linux script's `--force`.

**Verification caveat:** the `launchctl bootstrap`/`bootout`/`kickstart`/
`print` subcommands and `gui/$UID` domain targeting above were checked against
current documentation (Apple's `launchctl(1)`, corroborated by ss64.com and
community references) but not run against a real launchd instance from this
Linux session — exact exit codes and `print` output formatting should be
confirmed on real macOS hardware when this is implemented, per the same
verification standard the Linux side got by being tested against real
hardware in this session.

### Both platforms: default config seeding

Both `install.sh` scripts also seed `${XDG_CONFIG_HOME:-$HOME/.config}/blinkenkeys/`
from `examples/config/` (`config.yaml`, `templates/*.yaml`, `effects/*.yaml` —
not `examples/config/README.md`, which is a schema reference for humans, not
installer-owned content) — added after this spec's initial pass, once a
real-world install (a client's hook payloads all use named `state`s like
`claude/waiting`) showed a daemon with no config at all can't resolve any of
them, only raw colors.

This step runs unconditionally on every `install.sh` invocation but only ever
*creates*: it's gated on `${CONFIG_DIR}/config.yaml` not already existing, and
that gate is **not** affected by `--force` — unlike the binary/unit/plist
artifacts, this is user data the moment it's written (the same reasoning
`uninstall.sh` already applies by never removing `~/.config/blinkenkeys/`), so
a customized config is never clobbered by a later `--force` reinstall or
rebuild. The seeded `config.yaml`'s `devices:` entry still names the sample
device (`macropad`); the user edits it to match their own board's uid the same
way they would following `examples/config/README.md` directly — the installer
doesn't attempt to auto-detect and substitute the real device id, since that
would require the daemon to have already enumerated the device once, which
hasn't happened yet at seeding time.

### Testing / verification

`install.sh` performs real system mutations (installs a udev rule, changes
systemd `--user` state) that aren't meaningfully unit-testable without
mocking away the entire point of the script. Verification is:

- **Automated:** `shellcheck` and `shfmt` `prek` hooks over `packaging/**/*.sh`
  — the first shell scripts in this repo, so these are new hooks, not existing
  ones being extended. These apply to both platforms' scripts once they exist.
- **Manual, Linux (this pass):**
  `docs/superpowers/manual-checks/phase2-5-linux-install.md`, following the
  same pattern as the Phase 1+2 hardware round-trip doc. Covers: fresh install
  from a clean state, idempotent re-run (confirm no-op, no unnecessary `sudo`
  prompts), `--force` re-install, `uninstall.sh` removing all three artifacts
  and leaving user config/state untouched, and re-running `uninstall.sh` on an
  already-clean system (confirm no-op, no errors).
- **Manual, macOS (deferred to the user):** an equivalent
  `docs/superpowers/manual-checks/phase2-5-macos-install.md` is not written in
  this pass — the user will write and run it alongside their own
  implementation, covering the same cases (fresh install, idempotent re-run,
  `--force`, by-hand uninstall) plus confirming the `launchctl` reload-path
  reconciliation in step 7 actually behaves as specced.

## Explicitly deferred past this spec

- Writing `packaging/macos/install.sh`, `packaging/macos/uninstall.sh`, and
  the macOS manual-check doc, and testing all three against real
  launchd/macOS behavior (design is complete above for `install.sh`; the user
  will design, implement, and verify the macOS side on their own laptop).
- A root-fallback path in `install.sh` for environments where the udev rule
  can't be installed (e.g. no `sudo` access) — the design spec's warned-root-
  fallback (`warnIfRootFallback`) is a `blinkenkeysd` runtime concern, not an
  installer concern; the installer itself just errors if `sudo` fails.
- `config.Load` wiring into `main()` — unrelated pre-existing deferred item,
  not reopened here.
