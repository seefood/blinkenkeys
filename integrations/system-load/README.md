# System load integration

`blink-load.sh` — a tiny example showing blinkenkeys used for something
other than Claude Code: it polls CPU and RAM load and colors one key
green/orange/red accordingly. Linux-only (reads `/proc/loadavg` and
`/proc/meminfo` directly).

## Prerequisites

1. `blinkenkeysd` built and running, with your device declared in
   `config.yaml` (see `examples/config/`).
2. Find your device's name:
   ```
   curl -s --unix-socket "$HOME/.local/state/blinkenkeys/api.sock" http://localhost/devices
   ```

## Usage

```bash
BLINKENKEYS_DEVICE=my-macropad integrations/system-load/blink-load.sh
```

Runs in the foreground, polling every 5 seconds (`BLINKENKEYS_INTERVAL` to
change it) and `PUT`ing a color to a claimed key named `sysload`
(`BLINKENKEYS_KEY` to change it — see the Phase 5 named-key model in
`examples/config/README.md`'s "Addressing a key" section for what a bare
name means as a `{pos}`). On exit (Ctrl-C or SIGTERM) it releases the claim
via `DELETE`, blanking the key.

Thresholds (`max(cpu_load/nproc, mem_used) as a percentage`): green below
50%, orange 50–79%, red 80%+. Edit `color_for_pct()` in the script directly
if you want different cutoffs or colors — it's deliberately a plain `if`
chain, not a config option, since this is a starting point to fork, not a
feature of `blinkenkeysd` itself.

To run it persistently, wrap it in your own `systemd --user` unit or add it
to your shell profile's session startup — it's a plain foreground loop, no
packaging is provided for it (unlike `blinkenkeysd` itself).
