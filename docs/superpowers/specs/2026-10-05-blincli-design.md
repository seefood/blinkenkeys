# blincli design

Status: design draft, not implemented; the owner-tag and `keys.collision` policies are adopted. Unverified items are marked **[unverified]** and must be
checked against official docs before the code relying on them is written.

`blincli` is a client for `blinkenkeysd`'s HTTP API. It replaces hand-written `curl` calls in
hooks and scripts: it finds the daemon, finds the right key for the calling terminal tab, and
exposes the API's options as GNU-style flags. It never prompts (it usually runs as a hook).

## Commands

```
blincli [global options] <command> [command options]

  set       write a color / effect / template state to a key
  clear     blank a key and release it
  get       show a key's registration and current state
  devices   list devices, or show one device's LED layout
  detect    show what blincli would use (terminal, key, transport); sends nothing
  config    init | show | path
  version
```

Global options: `-S/--socket PATH`, `-u/--url URL`, `-t/--token TOKEN` (visible in `ps`; prefer
`--token-file F` or `$BLINKENKEYS_TOKEN`), `-d/--device REF`, `-C/--config FILE`, `-v/--verbose`,
`-q/--quiet`, `-h/--help`.

```
blincli set   [-k KEY | -n NAME] (-c COLOR | -e EFFECT | -s STATE) [-m N] [--if-detected]
blincli clear [-k KEY | -n NAME] [-m N] [--force] [--if-detected]
blincli get   [-k KEY | -n NAME | -a] [--json]
blincli devices [REF] [--json]
```

`set` takes exactly one of `-c/--color` (hex, `H,S,V` in QMK 0-255, or a CSS/X11 name),
`-e/--effect`, `-s/--state` — the API's exactly-one-of body. `-k/--key` takes any API `{pos}`
form (`R,C`, `led:N`, `idx:N`, or a name). `-n/--name NAME` forces a named (pooled) registration.
`-m/--slots N` is the tab-slot modulus (override only; the default comes from the device's key layout, see below, falling back to 6).
`--if-detected`: opt-in; exit 0 silently if no key can be derived (replaces the old
`[ -n "$WEZTERM_PANE" ] && ... || true` guard). Without it, an underivable key is an error.

Flag parsing: stdlib `flag`, one `FlagSet` per subcommand, the dual short/long registration
pattern already used by `cmd/blinkenkeysd`. No bundled short flags; flags precede positionals.

## Transport resolution (first match wins)

1. `--url` / `--socket`
2. `$BLINKENKEYS_URL` / `$BLINKENKEYS_SOCKET`
3. client config `blincli.yaml` (default `${XDG_CONFIG_HOME:-~/.config}/blinkenkeys/blincli.yaml`;
   separate from the daemon's `config.yaml`, which rejects unknown keys)
4. local socket probe: plain `dial` of the path from a local daemon `config.yaml`
   (`listeners.socket.path`) or `~/.local/state/blinkenkeys/api.sock`
5. none: print the setup help below to stderr, exit 78

```
blincli: no blinkenkeysd endpoint: no --url/--socket, no config at <path>, and no daemon socket at <path>

Create a config with one of:
  blincli config init                          write an annotated template, then edit it
  blincli config init --url http://HOST:49994 --token-file FILE [--device NAME]
  blincli config init --interactive            prompt for the values (requires a terminal)
or pass --url/--socket on each call, or set BLINKENKEYS_URL / BLINKENKEYS_SOCKET.
```

A socket file that exists but does not answer: "socket exists but daemon not answering", exit 69.
A token is required for `http(s)://` URLs, optional for sockets; missing -> exit 77 before
sending; daemon 401/403 -> exit 77.

`blincli.yaml` keys: `url` | `socket`, `token` | `token_file`, `device`, `slots`.

`config init` writes a template with every key commented out (an unedited template is
"unconfigured" and yields the error above), mode 0600, refuses to overwrite without
`-f/--force`. `--interactive/-i` exits 64 if stdin is not a TTY. The flag-driven form probes the
endpoint once and, if exactly one device exists, writes it as `device:`. `config show` masks
the token.

Device default: `-d` / `$BLINKENKEYS_DEVICE` > config `device:` > `GET /devices` if exactly one
device > exit 64 listing the candidates.

## Key resolution

Order, first match wins:

1. `-k` / `$BLINKENKEYS_KEY` — used verbatim.
2. `-n NAME` — named registration (pool claim) under NAME.
3. **Tab slot (direct write).** Detect the terminal; resolve its 1-based tab number N; key is
   `idx:((N-1) mod slots)`. `idx:` is the reading-order index over keyed LEDs, so on the
   reference device tabs 1-6 land on row 0 plus the first two row-1 keys (the user's
   Option-1..6 iTerm tab-switch keys); idx 6-11 are reserved by the user for other functions
   and are never written by tab slots. Tabs beyond `slots` wrap and can collide (see below).
4. **Named fallback.** No tab number obtainable: derive a name from the first unique id found
   in the environment, in this order: terminal instance id (`wezterm-17`, `kitty-4`, ...; see
   table), then `$BLINKENKEYS_NAME`, then `$CLAUDE_CODE_SESSION_ID` (verified: already used by
   `hooks-basic.json`), then other known session ids **[unverified]**: `$ZELLIJ_PANE_ID`,
   `$STY` (GNU screen), `$WT_SESSION` (Windows Terminal), `$TERM_SESSION_ID`. The name is then
   placed according to the device's layout:
   - ordinary named claim: the daemon auto-assigns the next free key from the layout's `pool`;
   - **shared** placement when the pool is empty or exhausted (daemon answers 409): the client
     writes directly to `tabs[fnv1a(name) mod len(tabs)]` with `owner` = the name. Same name
     always lands on the same key (no state, no leaked claim); collisions with tab slots or
     other names follow the owner-tag policy below.
5. **No unique id anywhere** (no `-k`/`-n`, nothing in the env above): error, exit 64, with a
   message listing `-k`, `-n`, and the env vars checked. Never guess an identity (no tty-path or
   pid heuristics). `--if-detected` turns this into a silent exit 0.

Remote transport: derived names/owners are prefixed with the short hostname
(`laptop.wezterm-17`) so panes on different machines do not collide. Names must not contain
`,` (it parses as `R,C`) or `/` (single path segment).

### Terminal resolvers (implement as many as feasible)

Each resolver returns `(tab number, owner id)` or only `(owner id)` for step 4. Order: most
specific first (tmux before the outer terminal, since e.g. `WEZTERM_PANE` is constant across
tmux panes).

| Terminal | Tab number | Instance id (fallback name) | Status |
|---|---|---|---|
| tmux | `tmux display -p -t $TMUX_PANE '#{window_index}'` | `$TMUX_PANE` | verified (`window_index` and `base-index` both work) |
| iTerm2 | `t` field of `$ITERM_SESSION_ID` (`w0t1p0:UUID`) | `w0t1p0` | zero-based (docs-verified); the env var is fixed when the shell starts, so it goes **stale** after tab reorder/close (accepted limitation); `wNtN` repeats across windows |
| WezTerm | tab position via `wezterm cli list --format json` | `$WEZTERM_PANE` (verified: monotonic mux-lifetime counter) | JSON shape verified (`window_id`, `tab_id`, `pane_id`); no tab-position field, so tab number = rank of `tab_id` in list order (list order = tab order still **[unverified]**, manual check 3) |
| kitty | `kitten @ ls` (needs remote control enabled) | `$KITTY_WINDOW_ID` | **[unverified]**; tab number likely skipped |

The resolver table lives in `internal/termid` so adding a terminal is one entry. Resolvers that
exec a helper bound it with a short timeout and treat failure as "no tab number" (fall to step 4).

### Collisions

Two sessions can map to one slot (tab 13 vs tab 1; `w0t0` vs `w1t0` in iTerm). Policy:
**last writer wins, with an owner tag.** A second, per-device policy governs a *direct*
(location-addressed) write landing on a key a **named claim** holds — see below.

- `set` carries an optional `owner` string (derived terminal identity, hostname-prefixed when
  remote). The engine's status record stores it; `get` displays it.
- A new write simply takes the key.
- `clear` is conditional: the CLI sends its owner, the daemon blanks/releases only if the recorded
  owner still matches; otherwise a no-op, exit 0. `--force` clears unconditionally. A session
  ending therefore never blanks a key another session has since taken.

#### Direct write vs. named claim (`keys.collision`)

Per device, in the `keys:` block:

- `last-wins` (default): the direct write takes the key; the named claim is released (its next
  write claims a fresh key).
- `displace`: the claim moves to the next unclaimed `pool` key (pool order), and its last request
  (color / effect / state, with its owner tag) is replayed there; a running effect restarts. If no
  pool key is free it degrades to `last-wins` and the daemon logs a warning. The incoming write is
  always applied, and a request with an invalid body displaces nothing.

Two direct writers on one key are always last-wins. Writes queued before a device's capabilities
are known always use `last-wins` (they are resolved under the registry lock, with no engine
access to replay a request).

## Per-device key layout (daemon `config.yaml`)

Every pad differs, so which keys play which role is configuration, not code. New optional
`keys:` block on a `devices:` entry (key lists use `idx:` reading-order numbers; ranges allowed):

```yaml
devices:
  - id: macropad
    keys:
      tabs: [0-5]        # tab N -> tabs[(N-1) mod len], in listed order
      pool: [6-11]       # named claims are auto-assigned from here (media keys may blink)
      collision: last-wins   # or displace; see Collisions
```

Key lists accept single indexes and ranges combined, as a YAML list or one string:
`[0-4, 6, 8-10]` or `"0-4,6,8-10"`. Order is preserved as written (it matters for `tabs`);
a range ascends. `pool: []` means no pool (named claims always use shared placement).

- Absent `keys:` keeps today's behavior (pool = every key with row >= 1; `tabs` unset, so the
  client falls back to idx 0..slots-1 with slots = 6). `keys:` present with `pool` omitted:
  today's default pool minus any `tabs` entries.
- The daemon enforces it: `nextUnclaimedLocked` only offers `pool` keys; writes the daemon
  receives for a key outside `tabs` and `pool` are still allowed (explicit `-k` always works).
- `GET /devices/{name}` (capabilities) gains `layout: {tabs, pool}` so a remote `blincli`
  needs no local layout config; `-m/--slots` and `slots:` only override.
- Config validation: indexes must be unique across `tabs` and `pool`. `idx:` values beyond the
  device's key count are **not** a hard validation (capabilities are unknown at config load):
  out-of-range `pool` entries are skipped at claim time, and a tab-slot write to an out-of-range
  index returns 404. Writes queued before capabilities are known (`applyPending`) always use
  `last-wins`, even when `collision: displace` is set.

## `get` and the API enhancement

```
$ blincli get
device    uid-440d6644c3b0438c  (connected)
key       idx:0 (tab 1)  led:5, row 1, col 2   owner laptop.iterm-w0t0
source    state claude/working
effect    breathe_orange   running, 12.4s elapsed, loops
color     #ff8000  (hsv 21,255,255)   desired value; keyboard RAM is not readable back
last set  12.4s ago  (2026-10-05T12:00:01Z)
```

`--json` prints the raw response; `-a/--all` lists every registered key. Exit 66 when the key is
not registered, so `blincli get -k X -q` is a registration test.

Daemon changes (not present today: the cache holds only the current color per LED, and the
engine drops finished effects and never records the original request):

1. `effects.Engine` (the only write path) keeps one status record per target: source type
   (`color`/`effect`/`state`), ref (`#ff8000` / `breathe_orange` / `claude/working`), `set_at`,
   `owner`, running-effect progress; it survives effect completion. `Timeline` gains a name and
   a nullable total duration. In-memory only, like the cache.
2. `GET /devices/{name}/keys/{pos}` — read-only; must not auto-claim (`Canonical` does), so the
   dispatcher needs a non-claiming lookup. 404 if a name has no claim, or a direct address has no
   ownership/status.
   ```json
   {"device":"uid-...","key":"wezterm-17","kind":"name","led":5,"row":1,"col":2,
    "connected":true,
    "color":{"h":21,"s":255,"v":255,"hex":"#ff8000"},
    "source":{"type":"state","ref":"claude/working","set_at":"...","age_ms":12400,"owner":"..."},
    "effect":{"name":"breathe_orange","running":true,"elapsed_ms":12400,"duration_ms":null},
    "claim":{"last_write":"...","expires_at":"..."}}
   ```
3. `GET /devices/{name}/keys` — array of the same for every registered key.
4. `PUT /devices/{name}/keys/{pos}` — optional `owner` body field.
5. `DELETE /devices/{name}/keys/{pos}` — extended to accept direct addresses (currently 400 for
   non-names): blanks the key; optional `?owner=` makes it conditional. Named keys still release
   the claim.

## Exit codes (sysexits; none is 2 — Claude Code hooks treat exit 2 as blocking on `Stop`/`UserPromptSubmit`, verified)

0 ok · 1 daemon returned an error · 64 usage / no key or device determinable · 66 key not
registered · 69 daemon unreachable · 77 auth missing/rejected · 78 no config/endpoint.

## Files

- `cmd/blincli/`, `internal/client/` (transport resolution, HTTP), `internal/termid/` (resolver
  table), plus the API/engine/dispatcher changes above.
- `make build` also builds `bin/blincli`; both platforms' `install.sh` install `blincli`
  alongside the daemon.
- `integrations/claude/hooks-blincli.json`: the five events of `hooks-basic.json` using plain
  `blincli set -s claude/{idle,working,waiting}` and `blincli clear` (no `--if-detected`: inside
  Claude Code `$CLAUDE_CODE_SESSION_ID` is always set, so a real misconfiguration should surface
  as a non-blocking hook error).
- `integrations/claude/README.md`: blincli section; `hooks-wezterm-pane.json` and its section
  stay as the raw-curl example.
- Daemon config: per-device key layout (next section).

## Open items

- Remaining **[unverified]** entries (WezTerm tab ordering, kitty, zellij/screen/Windows-Terminal
  variables): verify before implementing the corresponding piece.
