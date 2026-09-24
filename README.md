# blinkenkeys

A REST-controllable daemon that drives per-key RGB status indicators on Vial-based
mechanical keyboards, using VialRGB's "Direct" mode (host-controlled, RAM-only LED
colors, independent of the Vial app). Developed against the `cxt_studio/12e4` macropad -
[see this gist](https://gist.github.com/seefood/013529d4c17f449a09ff50a7bb596ad2)
for how that board's firmware got Vial + VialRGB support in the
first place — but designed to generalize to any VialRGB-capable device.

## Status

Language/implementation: **Go**. Specs:
[Phases 1–2](docs/superpowers/specs/2026-09-21-blinkenkeys-phase1-2-design.md) (POC → MVP)
and [Phase 3](docs/superpowers/specs/2026-09-24-blinkenkeys-phase3-effects-templates-design.md)
(effects, templates, server-owned timers). Phase 5 (per-client key allocation) is
also implemented, specced inline in Phase 3's "Named-key model" section; see
`CHANGELOG.md` for design revisions.

## Installation

Requires Go 1.27+, a C compiler (cgo), and on Linux the `libudev-dev`
headers. Build the binary first:

```bash
make build   # produces bin/blinkenkeysd
```

**Linux:** `packaging/linux/install.sh` installs the binary, a udev rule
granting the logged-in user unprivileged access to the device (no root
needed after the one-time rule install), and a `systemd --user` service
that starts `blinkenkeysd` at login. It's idempotent — safe to re-run
after a rebuild; pass `--force` to reinstall unconditionally.

```bash
packaging/linux/install.sh
```

`packaging/linux/uninstall.sh` reverses it (binary, service, udev rule)
without touching your `~/.config/blinkenkeys/` config or
`~/.local/state/blinkenkeys/` runtime data. See
[`docs/superpowers/manual-checks/phase2-5-linux-install.md`](docs/superpowers/manual-checks/phase2-5-linux-install.md)
for the full install/uninstall verification checklist.

**macOS:** designed but not implemented yet — see
[the Phase 2.5 design spec](docs/superpowers/specs/2026-09-24-blinkenkeys-phase2-5-service-installers-design.md).

## Scratching my itch

I got this keypad thinking I will use it with the encoders as jog wheels for video editing.
in the years since I got it I have been doing less editing and I forgot about it,
and now a year after ClaudeCode entered my life I found the need for a nice state indicator.
current selection on the market are little menu-bar indicators and iTerm integrations
that work only for local Claude terminals (I use it often via ssh) and they only show "working" or idle.

I want to know a very important parameter, [is my cache still valid?](https://gist.github.com/seefood/aa1cb93be7977f29373e8a856b5e94c0).
I work with the $20-level Claude subscription, so the cache TTL is 5 minutes. I to see
a 5 minute timer start the minute I hit "idle" and give me an indication if
I should rush to enter the next prompt at 10% price, or if I overslept and it's now
the 125% penalty and I can sit back and relax.

Rather than just solving my own problem, I decided to build a general purpose tool.
I'm planning to add templates to suport all sorts of use cases, please add your own via PR!

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

Windows support is an open question intentionally left for a future community PR —
not being built or tested here.

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
daemon on your LAN and let any of your coworkers changing your key colours,
feel free to patch it, but I don't condone it :)

## Known limitations

`blinkenkeysd` opens the device's raw-HID interface exclusively, the same way Vial's
own GUI does — only one process can hold that handle at a time. Running `blinkenkeysd`
and Vial (or `set_key_color.py`, or any other tool talking to the same interface)
against the same device simultaneously doesn't work; whichever opened it first keeps
it, and the other fails to open the device until the first one releases it.

VialRGB Direct-mode colors (`g_direct_mode_colors`) live in RAM only and are lost on
any firmware reset, USB replug, or brownout — the daemon has no way to read the
device's previous LED state back, only to (re)assert what it should be. This is why
the design keeps a host-side cache of last-set colors and periodically re-asserts it
(see the design spec). In theory this means you can unplug a device, connect it to
another port, and the daemon will recognize it up to 24 hours later and set the display
as it was, or as it has been updated since the disconnection.

## License

[GPL-3.0](LICENSE).
