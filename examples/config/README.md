# examples/config/

A ready-to-copy `${XDG_CONFIG_HOME:-~/.config}/blinkenkeys/` layout:

```
examples/config/
├── config.yaml       # sample top-level config (listeners, claims, devices)
├── effects/          # one *.yaml file per effect, filename = effect name
│   ├── breathe_blue.yaml
│   ├── breathe_orange.yaml
│   └── timer5min.yaml
└── templates/         # one *.yaml file per template, filename = template name
    └── claude.yaml
```

Copy (or symlink) `effects/`, `templates/`, and `config.yaml` into your real
config dir, then edit `config.yaml`'s `devices:` list to match your own
board. See `integrations/claude/README.md` for a worked example that wires
`templates/claude.yaml` into Claude Code hooks.

This schema reference is written against the actual Go source
(`internal/effects`, `config/config.go`), not the design specs — treat it as
current if the two ever disagree.

## `config.yaml`

All fields are optional; a missing key falls back to its default. Unknown
keys (top-level or nested) are a hard error, so don't leave stray fields
around. See `config.yaml` in this directory for a filled-in example.

```yaml
naming:
  prefer: uid          # "uid" (default), "path", or "vidpid" — falls back
                        # automatically per-device if the preferred kind
                        # isn't available for that device.

listeners:
  socket:
    path: ""            # "" = default ~/.local/state/blinkenkeys/api.sock
                         # (~ is expanded). Always on, mode 0600.
  tcp:                   # omit this whole block to disable (default: off)
    address: ":49994"    # no in-code default — must be set if tcp: is present
    token: "change-me"   # REQUIRED and non-empty whenever tcp: is present at all

devices:
  - id: "my-macropad"    # required, non-empty, not all-digits, unique
    optional: false      # true = writes to this name succeed even before
                          # the device has ever been seen, and it's exempt
                          # from untethered-device eviction

claims:
  idle_timeout: "8h"      # Go duration string; "" = default "8h"
```

## Effect files (`effects/*.yaml`)

One effect per file; **the effect's name is the filename without
`.yaml`**. Referenced from a template's `effect:` field or the REST API's
PUT body `{"effect": "<name>"}`.

```yaml
stages:
  - primitive: breathe
    duration: 3m                  # Go duration string; required on every
    settings:                     # stage except the last
      color: blue                 # hex "#rrggbb", "H,S,V" (0-255 each,
      frequency_hz: 0.5           # QMK-native scale), or a CSS/X11 name
      duty_cycle: 0.5              # 0..1
  - primitive: alternate
    settings:
      color_a: green
      color_b: red
      frequency_hz: 1.0
      duty_cycle: 0.2
      # no `duration` here: this is the last stage, so it's open-ended —
      # runs forever until superseded by the next write to this key
final_state: "#ff0000"            # only meaningful (and only required) if
                                   # every stage has a duration, i.e. the
                                   # whole timeline is finite
```

Each stage sets **exactly one** of:
- `color: <string>` — a flat, unanimated color for the stage.
- `primitive: <name>` + `settings: {...}` — see the primitives table below.
- `effect: <name>` — nests another effect by name (cycles are rejected). If
  this stage omits `duration`, it inherits the nested effect's own total
  duration; if the nested effect is itself open-ended, this stage may only
  be the last one in its own `stages` list.

### Primitives

| `primitive` | `settings` |
|---|---|
| `breathe` | `color`, `frequency_hz` (>0), `duty_cycle` (0..1) — V rises linearly for `duty_cycle` of the period, then falls for the rest |
| `alternate` | `color_a`, `color_b`, `frequency_hz` (>0), `duty_cycle` (0..1) — shows `color_a` for `duty_cycle` of the period, `color_b` for the rest |
| `blink` | `color`, `frequency_hz` (>0), `duty_cycle` (0..1) — same as `alternate` with `color_b` fixed to black |

All settings fields are required for the primitive used; unknown settings
keys are rejected.

### Color format

One shared parser, tried in order:
1. `"#rrggbb"` hex
2. `"H,S,V"` — three comma-separated 0-255 integers, QMK's native HSV scale
   (not 360°/100%/100%)
3. A CSS/X11 color name (case-insensitive), e.g. `blue`, `orange`

## Template files (`templates/*.yaml`)

One file per "program"; **the program's name is the filename without
`.yaml`**. The file is a flat map of state name → `{color: ...}` or
`{effect: ...}` (exactly one of the two):

```yaml
idle: {effect: timer5min}
working: {effect: breathe_orange}
waiting: {effect: timer5min}
build_ok: {color: green}     # a state can also be a plain literal color
```

State names (and template/effect filenames) must match `^[a-z0-9_-]+$`.

A template state is addressed as `"<program>/<state>"` — e.g.
`templates/claude.yaml`'s `idle:` entry is `claude/idle`, written via the
REST API's PUT body `{"state": "claude/idle"}`. There's no config-level
step that binds a device or key to a template; any `{name}` and `{pos}`
in the REST path can reference any `program/state` at write time.

## Addressing a key (`{pos}` in the REST API)

`PUT /devices/{name}/keys/{pos}` with a JSON body of exactly one of
`{"color": "..."}`, `{"effect": "..."}`, `{"state": "program/state"}`.
`{pos}` is one of:

- `R,C` — matrix row/column (e.g. `0,2`)
- `led:N` — raw VialRGB LED index (firmware order)
- `idx:N` — reading-order index over keyed LEDs
- anything else — a claimed key **name** (Phase 5's per-client pool; see
  `integrations/claude/README.md`)

`DELETE /devices/{name}/keys/{pos}` only works on a key **name** — it
releases the claim (and blanks the key) rather than writing a color.
