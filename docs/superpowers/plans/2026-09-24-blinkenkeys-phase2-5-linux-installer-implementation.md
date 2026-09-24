# blinkenkeys Phase 2.5 Linux installer — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a Linux-only install/uninstall flow for `blinkenkeysd`: a udev rule for unprivileged HID access, a `systemd --user` unit, and idempotent `install.sh`/`uninstall.sh` scripts, plus a manual verification checklist.

**Architecture:** Three checked-in template/script files under `packaging/linux/` (`99-blinkenkeys.rules`, `blinkenkeysd.service`, `install.sh`) plus a new `uninstall.sh` that reverses `install.sh`'s three artifacts. Both scripts are idempotent (compare-before-write) and shell out to `sudo` only for the single udev-rule line. `shellcheck`/`shfmt` prek hooks lint the scripts; real system-mutation behavior is verified by hand via a new manual-checks doc, not by automated tests.

**Tech Stack:** POSIX-ish bash, systemd `--user`, udev, `shellcheck` v0.11.0.1 (`shellcheck-py/shellcheck-py`), `shfmt` v3.14.1-1 (`scop/pre-commit-shfmt`, `shfmt` hook id — prebuilt binary via `language: python`, not the `shfmt-src`/`shfmt-docker` variants).

**Spec:** `docs/superpowers/specs/2026-09-24-blinkenkeys-phase2-5-service-installers-design.md` (Linux sections only: "Linux: udev rule", "Linux: systemd `--user` unit", "Linux: binary install location", "Linux: `install.sh`", "Linux: `uninstall.sh`", "Testing / verification"'s Linux bullet). macOS sections are explicitly out of scope for this plan — the user is implementing `packaging/macos/install.sh`/`uninstall.sh` separately on their own machine.

## Global Constraints

- Udev rule matches `ATTRS{serial}=="vial:*"` (firmware-build fingerprint, not per-device) — never widen this to match on VID/PID or an exact serial.
- Udev rule installs as `/etc/udev/rules.d/99-blinkenkeys.rules` — a name distinct from the user's pre-existing, hand-edited `/etc/udev/rules.d/99-vial.rules`. Never read, write, or overwrite `99-vial.rules`.
- Binary install location: `${XDG_BIN_HOME:-$HOME/.local/bin}/blinkenkeysd`.
- Unit/rule/plist content ships as separate template files (`packaging/linux/*.service`, `*.rules`), never as heredocs embedded in the scripts.
- `__BLINKENKEYSD_BIN__` is the literal placeholder token in `blinkenkeysd.service`; `install.sh` substitutes it with the resolved `$BIN_DIR/blinkenkeysd` path before writing.
- `install.sh`/`uninstall.sh` are idempotent by default: each artifact is compared against what's already installed (`cmp -s` for files) and only rewritten/removed if different/present, unless `--force` (install.sh only — uninstall.sh has no `--force`, removal is already idempotent by nature).
- Only the udev-rule install/remove lines and `udevadm control`/`trigger` may use `sudo`; every other step runs as the normal user. No single coarse `sudo` wrapper around the whole script.
- `systemctl --user enable --now` does not restart an already-running unit — `install.sh` must explicitly `systemctl --user restart` when the binary or unit changed and the service was already active.
- No root-fallback flow in either script — if `sudo` fails, the script errors and stops.
- `uninstall.sh` never deletes `${XDG_CONFIG_HOME:-$HOME/.config}/blinkenkeys` (user config/effects/templates) or `~/.local/state/blinkenkeys` (socket/cache dir) — those are user data, not installer-owned artifacts.
- `bin/blinkenkeysd` must already exist (built via `make build`) before `install.sh` runs — the script errors with a message pointing at `make build` rather than building it itself.

## Review Focus

- **Running `install.sh` twice in a row with no changes**: must be a true no-op — no `sudo` prompt, no `systemctl restart`, exit 0. This is the idempotency guarantee the whole design rests on; an accidental restart or repeated `sudo install` on every run would make the script unsafe to re-run from a cron job or a `make install` alias.
- **`uninstall.sh` run on a system where nothing was ever installed**: must not error (e.g. `systemctl --user disable --now` on a nonexistent unit, `rm` on a nonexistent file) — every step needs an existence check first, matching the spec's explicit idempotency requirement for the uninstaller.
- **`install.sh --force` when the service is already running**: must still end with the *new* binary actually running (via `restart`), not just re-copied on disk while the old process keeps serving — this is the "silently keep running the old binary" failure the spec calls out by name.
- **A concurrent, unrelated `/etc/udev/rules.d/99-vial.rules`**: the installer/uninstaller must never touch it — worth an explicit assertion in the manual-check doc, not just an absence of code that touches it, since a typo'd glob or wildcard `rm` would be easy to miss in review.
- **`$BIN_DIR` or `$XDG_CONFIG_HOME`-derived paths containing no pre-existing parent directory** (fresh machine, first-ever install): `mkdir -p` must be used everywhere a destination directory is assumed to exist, not just for `$BIN_DIR` — the systemd user-unit directory (`~/.config/systemd/user/`) commonly doesn't exist until the first unit is ever installed.

---

## Task 1: shellcheck/shfmt prek hooks for `packaging/**/*.sh`

**Files:**
- Modify: `.pre-commit-config.yaml`

**Interfaces:**
- Consumes: nothing from earlier tasks (first task).
- Produces: `prek run --all-files` now lints any `packaging/**/*.sh` file added in later tasks. Later tasks rely on this to catch shell issues at commit time.

- [ ] **Step 1: Add the two new hook repos to `.pre-commit-config.yaml`**

Append to the end of the existing `repos:` list:

```yaml
  - repo: https://github.com/shellcheck-py/shellcheck-py
    rev: v0.11.0.1
    hooks:
      - id: shellcheck

  - repo: https://github.com/scop/pre-commit-shfmt
    rev: v3.14.1-1
    hooks:
      - id: shfmt
```

- [ ] **Step 2: Verify prek accepts the new config**

Run: `prek run --all-files`
Expected: exits 0. The Go hooks continue to run against existing `.go` files; `shellcheck`/`shfmt` report "no files to check" (or are silently skipped) since no `.sh` files exist yet — this is expected and matches the pattern already used for the "forward-looking" Go hooks noted at the top of the file.

- [ ] **Step 3: Commit**

```bash
git add .pre-commit-config.yaml
git commit -m "$(cat <<'EOF'
Add shellcheck/shfmt prek hooks for upcoming packaging/ scripts

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: udev rule and systemd unit templates

**Files:**
- Create: `packaging/linux/99-blinkenkeys.rules`
- Create: `packaging/linux/blinkenkeysd.service`

**Interfaces:**
- Consumes: nothing.
- Produces: two static template files. `install.sh` (Task 3) reads both verbatim (the rule) or with one substitution (the unit, `__BLINKENKEYSD_BIN__` → resolved binary path). `uninstall.sh` (Task 4) only needs to know the *destination* paths these templates get installed to (`/etc/udev/rules.d/99-blinkenkeys.rules`, `${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/blinkenkeysd.service`), not their contents.

- [ ] **Step 1: Write the udev rule template**

`packaging/linux/99-blinkenkeys.rules`:

```
KERNEL=="hidraw*", SUBSYSTEM=="hidraw", ATTRS{serial}=="vial:*", MODE="0660", TAG+="uaccess"
```

- [ ] **Step 2: Write the systemd unit template**

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

- [ ] **Step 3: Verify file contents are byte-exact**

Run: `cat packaging/linux/99-blinkenkeys.rules packaging/linux/blinkenkeysd.service`
Expected: output matches Steps 1–2 exactly (no trailing typos, no accidental tab/space substitution in the `.rules` file's `==`/`+=` operators — udev is strict about this syntax).

- [ ] **Step 4: Commit**

```bash
git add packaging/linux/99-blinkenkeys.rules packaging/linux/blinkenkeysd.service
git commit -m "$(cat <<'EOF'
Add Linux udev rule and systemd --user unit templates for blinkenkeysd

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `install.sh`

**Files:**
- Create: `packaging/linux/install.sh`

**Interfaces:**
- Consumes: `packaging/linux/99-blinkenkeys.rules`, `packaging/linux/blinkenkeysd.service` (Task 2, read verbatim/substituted); `bin/blinkenkeysd` (pre-built, not produced by this plan).
- Produces: `$BIN_DIR/blinkenkeysd`, `${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/blinkenkeysd.service`, `/etc/udev/rules.d/99-blinkenkeys.rules` on the machine it's run on. `uninstall.sh` (Task 4) targets exactly these three paths.

- [ ] **Step 1: Write `packaging/linux/install.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
RULES_DEST="/etc/udev/rules.d/99-blinkenkeys.rules"
SERVICE_NAME="blinkenkeysd.service"

FORCE=0
if [[ "${1:-}" == "--force" ]]; then
  FORCE=1
fi

bin_changed=0
unit_changed=0
rule_changed=0

mkdir -p "$BIN_DIR" "$UNIT_DIR"

SRC_BIN="$SCRIPT_DIR/../../bin/blinkenkeysd"
if [[ ! -f "$SRC_BIN" ]]; then
  echo "error: $SRC_BIN not found — run 'make build' first" >&2
  exit 1
fi

DEST_BIN="$BIN_DIR/blinkenkeysd"
if [[ "$FORCE" -eq 1 ]] || ! cmp -s "$SRC_BIN" "$DEST_BIN" 2>/dev/null; then
  cp "$SRC_BIN" "$DEST_BIN"
  chmod +x "$DEST_BIN"
  bin_changed=1
fi

RENDERED_UNIT="$(mktemp)"
trap 'rm -f "$RENDERED_UNIT"' EXIT
sed "s|__BLINKENKEYSD_BIN__|$DEST_BIN|" "$SCRIPT_DIR/blinkenkeysd.service" >"$RENDERED_UNIT"

DEST_UNIT="$UNIT_DIR/$SERVICE_NAME"
if [[ "$FORCE" -eq 1 ]] || ! cmp -s "$RENDERED_UNIT" "$DEST_UNIT" 2>/dev/null; then
  cp "$RENDERED_UNIT" "$DEST_UNIT"
  unit_changed=1
fi

if [[ "$FORCE" -eq 1 ]] || ! cmp -s "$SCRIPT_DIR/99-blinkenkeys.rules" "$RULES_DEST" 2>/dev/null; then
  sudo install -m 0644 "$SCRIPT_DIR/99-blinkenkeys.rules" "$RULES_DEST"
  sudo udevadm control --reload-rules
  sudo udevadm trigger
  rule_changed=1
fi

was_active=0
if systemctl --user is-active --quiet "$SERVICE_NAME"; then
  was_active=1
fi

if [[ "$unit_changed" -eq 1 ]]; then
  systemctl --user daemon-reload
fi

systemctl --user enable --now "$SERVICE_NAME"

if [[ "$was_active" -eq 1 ]] && { [[ "$bin_changed" -eq 1 ]] || [[ "$unit_changed" -eq 1 ]]; }; then
  systemctl --user restart "$SERVICE_NAME"
fi

echo
echo "binary:    $([[ $bin_changed -eq 1 ]] && echo "installed to $DEST_BIN" || echo "already up to date")"
echo "unit:      $([[ $unit_changed -eq 1 ]] && echo "installed to $DEST_UNIT" || echo "already up to date")"
echo "udev rule: $([[ $rule_changed -eq 1 ]] && echo "installed to $RULES_DEST" || echo "already up to date")"
echo
systemctl --user status "$SERVICE_NAME" --no-pager
```

- [ ] **Step 2: Make it executable**

Run: `chmod +x packaging/linux/install.sh`

- [ ] **Step 3: Syntax-check**

Run: `bash -n packaging/linux/install.sh`
Expected: no output, exit 0.

- [ ] **Step 4: Run shellcheck directly (faster feedback than committing to trigger the hook)**

Run: `shellcheck packaging/linux/install.sh`
Expected: no warnings. If shellcheck isn't installed locally, skip this step — Step 6's commit will run it via prek instead, using the pinned version from Task 1.

- [ ] **Step 5: Run shfmt directly**

Run: `shfmt -l packaging/linux/install.sh`
Expected: no output (already correctly formatted). If shfmt reports the file, run `shfmt -w packaging/linux/install.sh` and re-check. If shfmt isn't installed locally, skip — Step 6's commit runs it via prek.

- [ ] **Step 6: Commit**

```bash
git add packaging/linux/install.sh
git commit -m "$(cat <<'EOF'
Add Linux install.sh: idempotent binary/unit/udev-rule installer

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```
Expected: the `shellcheck`/`shfmt`/`trailing-whitespace`/`end-of-file-fixer` prek hooks all pass on `packaging/linux/install.sh`. If a hook fails, fix the reported issue and re-commit — do not skip hooks.

---

## Task 4: `uninstall.sh`

**Files:**
- Create: `packaging/linux/uninstall.sh`

**Interfaces:**
- Consumes: the same three destination paths `install.sh` (Task 3) writes to — `$BIN_DIR/blinkenkeysd`, `${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/blinkenkeysd.service`, `/etc/udev/rules.d/99-blinkenkeys.rules` — computed the same way (`XDG_BIN_HOME`/`XDG_CONFIG_HOME` fallback logic must match `install.sh` exactly, or a rule/unit installed by one will not be found by the other).
- Produces: a clean system (none of the three artifacts present); nothing later consumes this script's output.

- [ ] **Step 1: Write `packaging/linux/uninstall.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

BIN_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
RULES_DEST="/etc/udev/rules.d/99-blinkenkeys.rules"
SERVICE_NAME="blinkenkeysd.service"

DEST_BIN="$BIN_DIR/blinkenkeysd"
DEST_UNIT="$UNIT_DIR/$SERVICE_NAME"

service_removed=0
unit_removed=0
rule_removed=0
bin_removed=0

if systemctl --user is-enabled --quiet "$SERVICE_NAME" 2>/dev/null || systemctl --user is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
  systemctl --user disable --now "$SERVICE_NAME"
  service_removed=1
fi

if [[ -f "$DEST_UNIT" ]]; then
  rm -f "$DEST_UNIT"
  systemctl --user daemon-reload
  unit_removed=1
fi

if [[ -f "$RULES_DEST" ]]; then
  sudo rm -f "$RULES_DEST"
  sudo udevadm control --reload-rules
  sudo udevadm trigger
  rule_removed=1
fi

if [[ -f "$DEST_BIN" ]]; then
  rm -f "$DEST_BIN"
  bin_removed=1
fi

echo
echo "service:   $([[ $service_removed -eq 1 ]] && echo "disabled and stopped" || echo "was not loaded")"
echo "unit:      $([[ $unit_removed -eq 1 ]] && echo "removed ($DEST_UNIT)" || echo "already absent")"
echo "udev rule: $([[ $rule_removed -eq 1 ]] && echo "removed ($RULES_DEST)" || echo "already absent")"
echo "binary:    $([[ $bin_removed -eq 1 ]] && echo "removed ($DEST_BIN)" || echo "already absent")"
```

- [ ] **Step 2: Make it executable**

Run: `chmod +x packaging/linux/uninstall.sh`

- [ ] **Step 3: Syntax-check**

Run: `bash -n packaging/linux/uninstall.sh`
Expected: no output, exit 0.

- [ ] **Step 4: Run shellcheck and shfmt directly (same caveat as Task 3 if not installed locally)**

Run: `shellcheck packaging/linux/uninstall.sh && shfmt -l packaging/linux/uninstall.sh`
Expected: no warnings, no formatting diff.

- [ ] **Step 5: Commit**

```bash
git add packaging/linux/uninstall.sh
git commit -m "$(cat <<'EOF'
Add Linux uninstall.sh: idempotent removal of install.sh's three artifacts

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```
Expected: prek hooks pass, same as Task 3 Step 6.

---

## Task 5: Manual verification checklist

**Files:**
- Create: `docs/superpowers/manual-checks/phase2-5-linux-install.md`

**Interfaces:**
- Consumes: `packaging/linux/install.sh` and `packaging/linux/uninstall.sh` (Tasks 3–4), run by a human (or an agent with explicit per-step permission, since every step here mutates real system state — installs a udev rule, changes `systemd --user` state).
- Produces: nothing consumed by later tasks — this is a leaf/terminal artifact, same role as the existing `phase1-2-hardware-roundtrip.md` and `phase3-effects.md` docs.

- [ ] **Step 1: Write the checklist**

`docs/superpowers/manual-checks/phase2-5-linux-install.md`:

```markdown
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
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/manual-checks/phase2-5-linux-install.md
git commit -m "$(cat <<'EOF'
Add Phase 2.5 Linux install/uninstall manual verification checklist

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: CHANGELOG entry

**Files:**
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: nothing (documentation-only task, last in the plan).
- Produces: nothing consumed by other tasks.

- [ ] **Step 1: Add a new "Phase 2.5 design" section**

Append to `CHANGELOG.md`, following the existing per-spec section convention:

```markdown

## Phase 2.5 design (`docs/superpowers/specs/2026-09-24-blinkenkeys-phase2-5-service-installers-design.md`)

- **2026-09-24**: Added a Linux `uninstall.sh` to the spec (originally listed
  under "Explicitly deferred past this spec") and implemented it alongside
  `install.sh` — reverses each of the three artifacts `install.sh` installs
  (binary, systemd unit, udev rule), idempotent, and leaves user
  config/state directories untouched.
```

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "$(cat <<'EOF'
Document Phase 2.5 spec's in-scope uninstall.sh addition in CHANGELOG

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```
