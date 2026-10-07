# blinkenkeys

**Turn keys on a programmable keyboard into status lights you define.** Point any
script, hook or CI job at a key with one command and choose its color or
animation, from a solid color to a timed multi-stage countdown. Works with any
VialRGB keyboard.

*My own use: one key per Claude Code terminal tab, breathing while Claude works
and counting down the 5-minute prompt-cache window once it goes idle.*

<!-- TODO: intro/demo video goes here -->

## Why

I got this keypad thinking I would use the encoders as jog wheels for video editing.
In the years since I have been doing less editing and I forgot about it,
and now a year after Claude Code entered my life I found the need for a nice state indicator.
The current selection on the market is little menu-bar indicators and iTerm integrations
that work only for local Claude terminals (I use it often via ssh) and they only show "working" or idle.

I want to know a very important parameter, [is my cache still valid?](https://gist.github.com/seefood/aa1cb93be7977f29373e8a856b5e94c0).
I work with the $20-level Claude subscription, so the cache TTL is 5 minutes. I want to see
a 5 minute timer start the minute I hit "idle" and give me an indication if
I should rush to enter the next prompt at 10% price, or if I overslept and it's now
the 125% penalty and I can sit back and relax.

Rather than just solving my own problem, I decided to build a general purpose tool.
Add your own templates for other use cases via PR!

## Quick start

On macOS:

```bash
brew tap seefood/blinkenkeys
brew install blinkenkeys
brew services start blinkenkeys
blincli set -s claude/idle   # state on this terminal's key
```

The service seeds `~/.config/blinkenkeys` from the example config on first
start. You will likely need to edit its `devices:` entry to match your own
board. Linux and other install routes are under [Installation](#installation).

## Make it yours

`blinkenkeysd` is a small daemon that owns the keyboard's LEDs; everything that
decides what a key looks like is YAML in `~/.config/blinkenkeys/`:

- **Effects** (`effects/*.yaml`): timelines of stages built from `breathe`,
  `blink` and `alternate` primitives or flat colors. A stage with a duration
  makes the countdown run entirely server-side: one "went idle" call, and the
  key animates by itself afterwards.
- **Templates** (`templates/*.yaml`): name your own states and map each to a
  color or an effect, then set them with `blincli set -s <program>/<state>` or
  the REST API.

The bundled `claude/*` states are just one example set, shaped for my Claude
Code setup. Define whatever colors and animations suit your own integrations.
The schema reference and ready-to-copy samples are in
[`examples/config/`](examples/config/README.md), and
[`integrations/`](integrations/) has hook examples.

## Installation

**Homebrew (macOS):** the tap installs the prebuilt release binaries
(`blinkenkeysd` and `blincli`) and a login service:

```bash
brew tap seefood/blinkenkeys
brew install blinkenkeys
brew services start blinkenkeys
```

On first start the service seeds `~/.config/blinkenkeys` from the example
config, only if `config.yaml` is not already there; an existing config is never
overwritten. Check it with `blinkenkeysd --check-config`, and read the daemon
log at `$(brew --prefix)/var/log/blinkenkeysd.log`. `blincli` lands in
`$(brew --prefix)/bin`. The tap follows the latest non-prerelease GitHub release, checked daily.
Linux raw-HID access needs a udev rule that Homebrew cannot install; on Linux
use the release tarball below.

**From a release tarball:** download the archive for your OS/arch and
`SHA256SUMS` from the [releases page](https://github.com/seefood/blinkenkeys/releases),
then verify, unpack and run the installer for your OS (below) from the
extracted directory:

```bash
sha256sum -c --ignore-missing SHA256SUMS   # macOS: shasum -a 256 -c --ignore-missing SHA256SUMS
tar xzf blinkenkeys-vX.Y.Z-<os>-<arch>.tar.gz
cd blinkenkeys-vX.Y.Z-<os>-<arch>
```

macOS binaries are unsigned: after a browser download, run
`xattr -dr com.apple.quarantine .` in the extracted directory first.

**From source:** requires Go 1.27+, a C compiler (cgo), and on Linux the
`libudev-dev` headers. Build the binary first:

```bash
make build   # produces bin/blinkenkeysd and bin/blincli
```

**Linux:** `packaging/linux/install.sh` installs the binary, a udev rule
granting the logged-in user unprivileged access to the device (no root
needed after the one-time rule install), and a `systemd --user` service
that starts `blinkenkeysd` at login. It also seeds
`${XDG_CONFIG_HOME:-~/.config}/blinkenkeys/` from `examples/config/` the
first time (never overwriting a config you've already customized, even with
`--force`) — edit its `devices:` entry to match your own board's uid. It's
idempotent — safe to re-run after a rebuild; pass `--force` to reinstall the
binary/unit/rule unconditionally.

```bash
packaging/linux/install.sh
```

`packaging/linux/uninstall.sh` reverses it (binary, service, udev rule)
without touching your `~/.config/blinkenkeys/` config or
`~/.local/state/blinkenkeys/` runtime data. See
[`docs/superpowers/manual-checks/phase2-5-linux-install.md`](docs/superpowers/manual-checks/phase2-5-linux-install.md)
for the full install/uninstall verification checklist.

**macOS:** `packaging/macos/install.sh` installs the binary and a
`launchd` LaunchAgent (`~/Library/LaunchAgents/com.seefood.blinkenkeysd.plist`)
that starts `blinkenkeysd` at login — no root, no TCC grant needed (see
[the Phase 2.5 design spec](docs/superpowers/specs/2026-09-24-blinkenkeys-phase2-5-service-installers-design.md)).
It also seeds `${XDG_CONFIG_HOME:-~/.config}/blinkenkeys/` from
`examples/config/` the first time (never overwriting a config you've already
customized, even with `--force`) — edit its `devices:` entry to match your
own board's uid. It's idempotent — safe to re-run after a rebuild; pass
`--force` to reinstall the binary/plist unconditionally.

```bash
packaging/macos/install.sh
```

`packaging/macos/uninstall.sh` reverses it (binary, LaunchAgent) without
touching your `~/.config/blinkenkeys/` config or
`~/.local/state/blinkenkeys/` runtime data. See
[`docs/superpowers/manual-checks/phase2-5-macos-install.md`](docs/superpowers/manual-checks/phase2-5-macos-install.md)
for the full install/uninstall verification checklist.

## blincli

`blincli` is a small pure-Go client for the daemon's API; it finds the
daemon, picks a key from your terminal tab, and is what Claude Code hooks
call:

```bash
blincli set -s claude/idle   # state on this terminal's key
blincli get                  # what is on this key, and for how long
```

`make build` produces it as `bin/blincli` and the installers put it next to
`blinkenkeysd`. Each device can declare a key layout (`keys.tabs` for terminal
tab slots, `keys.pool` for named claims) and a `keys.collision` policy:
`last-wins` (default) or `displace`. See
[`integrations/claude/README.md`](integrations/claude/README.md) and the
[design spec](docs/superpowers/specs/2026-10-05-blincli-design.md).

## Networking

`blinkenkeysd` always binds a `$HOME`-owned Unix domain socket (mode `0600`) for
local clients — reachable elegantly from shell/curl via `curl --unix-socket <path>
http://localhost/...` (supported natively since curl 7.40, no extra tooling needed).
It can *optionally* also bind a TCP listener (default `:49994`; e.g. for reaching the
daemon from a remote SSH session or server back to the machine the keyboard is physically
attached to) — when TCP is enabled, a bearer token is mandatory (not just optional),
since filesystem permissions no longer provide the access control. Plain HTTP + token
is the accepted threat model for now (LAN/trusted-network use); SSH port forwarding
is the documented escape hatch if stronger transport security is ever needed, rather
than adding TLS to `blinkenkeysd` itself. If you feel good about running an open
daemon on your LAN and let any of your cow orkers changing your key colours,
feel free to patch it, but I don't condone it :)

See
[`docs/superpowers/manual-checks/tcp-listener.md`](docs/superpowers/manual-checks/tcp-listener.md)
for the TCP listener's manual verification checklist.

## Known limitations

`blinkenkeysd` opens the device's raw-HID interface exclusively, the same way Vial's
own GUI does — only one process can hold that handle at a time. Running `blinkenkeysd`
and Vial (or `set_key_color.py`, or any other tool talking to the same interface)
against the same device simultaneously doesn't work; whichever opened it first keeps
it, and the other fails to open the device until the first one releases it.

To free the device for Vial without uninstalling the service:

- **Linux:** `systemctl --user stop blinkenkeysd.service`, then
  `systemctl --user start blinkenkeysd.service` (or just
  `packaging/linux/install.sh`) when you're done.
- **macOS:** `launchctl bootout "gui/$(id -u)/com.seefood.blinkenkeysd"`, then
  `launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/com.seefood.blinkenkeysd.plist`
  (or just `packaging/macos/install.sh`) when you're done. Note `bootout` only
  stops it for the current login session — since the LaunchAgent has
  `RunAtLoad`, it comes back automatically on your next login/reboot.

VialRGB Direct-mode colors (`g_direct_mode_colors`) live in RAM only and are lost on
any firmware reset, USB replug, or brownout — the daemon has no way to read the
device's previous LED state back, only to (re)assert what it should be. This is why
the design keeps a host-side cache of last-set colors and periodically re-asserts it
(see the design spec). In theory this means you can unplug a device, connect it to
another port, and the daemon will recognize it up to 24 hours later and set the display
as it was, or as it has been updated since the disconnection.

## Roadmap

1. **Set a key's color** — POC. One HID device, one REST call, sets one key/LED to a
   given color (hex, HSV, or named color).
2. **Device enumeration** — MVP. `GET` endpoints report all connected Vial-capable
   devices (up to several at once), their matrix size/LED capabilities, and
   config-assigned stable names (so USB renumbering / port changes don't break
   clients). Duplicate boards get auto-suffixed names (`-0`, `-1`, ...).
3. **Effects, abstraction templates, server-owned timers** — Go-coded animation
   primitives (breathe, blink, two-color alternation) composed into named
   multi-stage effects in `~/.config/blinkenkeys/effects/*.yaml`, and templates in
   `~/.config/blinkenkeys/templates/*.yaml` mapping semantic states
   (`claude/idle`, mic-mute, build-status, ...) to colors or effects. A
   multi-stage effect with timed stages *is* a server-owned timer: a Claude Code
   hook says "went idle" once, and the key animates toward "cache about to
   expire" over the next 5 minutes entirely server-side. See
   `examples/config/`.
4. *(Folded into Phase 3.)* Radius-based "explosion" / multi-key effects are on
   hold.
5. **Per-client key allocation** — done. A subscriber (e.g. a coding agent
   instance) is allocated a key by name and directs its own state to it; the
   pool auto-assigns the next unclaimed key (ascending row/col order) and
   releases on explicit `DELETE` or an idle timeout. Terminal-pane
   correlation (WezTerm) has a first cut: see
   `integrations/claude/hooks-wezterm-pane.json` — a direct `R,C` write to
   row 0, column `$WEZTERM_PANE % <row-0 width>`, alongside the per-session
   pooled key. iTerm and a less collision-prone mapping (`$WEZTERM_PANE` is
   a mux-lifetime counter, not a small stable index) are still open.
6. *(Folded into Phase 3 — see item 3.)*

7. **Command-triggered effects** — future work, not yet designed. Selecting
   an effect (or reaching a particular stage of one) could also fire an
   arbitrary command, e.g. playing a sound when a countdown effect like
   `timer5min` finishes. Would need a design pass on where the command
   lives in the effect/template schema and what's allowed to trigger it
   (security-sensitive: this is arbitrary command execution, so it needs a
   real opt-in, not just a config field).
8. **Friendly CLI** — initial version included. A wrapper that
   encapsulates pane detection (see item 5's WezTerm/iTerm correlation) in
   user-facing terms instead of raw `R,C` math, reads the bearer token from
   the daemon's own config so the user doesn't have to, and calls `curl`
   against the socket/TCP listener in the background on the user's behalf.

Windows support is an open question intentionally left for a future community PR, since I don't have windows machines available, so this is
not being built or tested here.

## Status

Developed against the `cxt_studio/12e4` macropad -
[see this gist](https://gist.github.com/seefood/013529d4c17f449a09ff50a7bb596ad2)
for how that board's firmware got Vial + VialRGB support in the first place -
but designed to generalize to any VialRGB-capable device. It drives the keys
through VialRGB's "Direct" mode (host-controlled, RAM-only LED colors,
independent of the Vial app).

Language/implementation: **Go**. Specs:
[Phases 1–2](docs/superpowers/specs/2026-09-21-blinkenkeys-phase1-2-design.md) (POC → MVP)
and [Phase 3](docs/superpowers/specs/2026-09-24-blinkenkeys-phase3-effects-templates-design.md)
(effects, templates, server-owned timers). Phase 5 (per-client key allocation) is
also implemented, specced inline in Phase 3's "Named-key model" section; see
`CHANGELOG.md` for design revisions.

## License

[GPL-3.0](LICENSE).
