# blinkenkeys

A REST-controllable daemon that drives per-key RGB status indicators on Vial-based
mechanical keyboards, using VialRGB's "Direct" mode (host-controlled, RAM-only LED
colors, independent of the Vial app). Developed against the `cxt_studio/12e4` macropad -
[see this gist](https://gist.github.com/seefood/013529d4c17f449a09ff50a7bb596ad2)
for how that board's firmware got Vial + VialRGB support in the
first place — but designed to generalize to any VialRGB-capable device.

## Status

Language/implementation: **Go**. Active spec:
[`docs/superpowers/specs/2026-09-21-blinkenkeys-phase1-2-design.md`](docs/superpowers/specs/2026-09-21-blinkenkeys-phase1-2-design.md)
covers Phases 1–2 (POC → MVP). Phases 3–6 below are roadmap/future-proofing only —
not yet speced in detail, but the concurrency/dispatcher design was made now
specifically so it doesn't require rewrites later.

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
3. **Abstraction templates** — named semantic states (mic-mute, build-status, DND, ...)
   mapped to phase 3 effects/colors via YAML config dropped in
   `~/.config/blinkenkeys/templates/`.
4. **Effects** — blink, breathe, two-color alternation, radius-based "explosion"
   propagation from a key, etc. Client requests an effect; `blinkenkeysd` owns the
   animation timing loop.
5. **Per-client key allocation** — a subscriber (e.g. a coding agent instance) is
   allocated a key and directs its own state to it. Open question, unsolved: how to
   correlate a subscriber to a specific terminal tab (iTerm/WezTerm) automatically.
6. **Server-owned timers** — client fires a single "entered state X" event;
   `blinkenkeysd` runs the clock and animates the passage of time itself (motivating
   example: a Claude Code hook says "went idle," and the keyboard animates toward a
   "cache about to expire" warning over the following 5 minutes, entirely
   server-side). Templatable per phase 4's YAML mechanism.

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

VialRGB Direct-mode colors (`g_direct_mode_colors`) live in RAM only and are lost on
any firmware reset, USB replug, or brownout — the daemon has no way to read the
device's previous LED state back, only to (re)assert what it should be. This is why
the design keeps a host-side cache of last-set colors and periodically re-asserts it
(see the design spec). In theory this means you can unplug a device, connect it to
another port, and the daemon will recognize it up to 24 hours later and set the display
as it was, or as it has been updated since the disconnection.
