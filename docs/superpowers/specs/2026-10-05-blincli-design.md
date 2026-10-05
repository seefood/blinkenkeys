# blincli design

Status: design draft, not implemented. Unverified items are marked **[unverified]** and must be
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
`-m/--slots N` is the tab-slot modulus (default 12; `slots:` in the client config).
`--if-detected`: exit 0 silently if no key can be derived (replaces the old
`[ -n "$WEZTERM_PANE" ] && ... || true` guard).

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
   Option-1..6 iTerm tab-switch keys). Tabs beyond `slots` wrap and can collide (see below).
4. **Named registration fallback.** Terminal detected but no tab number obtainable: claim a pool
   key under a name derived from the terminal's pane/window id, e.g. `wezterm-17`.
5. **Undecided** (open question): neither a tab number nor any terminal-instance id.
   Interim behavior: exit 64 with a hint to pass `-k`/`-n`, or exit 0 silently under
   `--if-detected`. Candidate identities to evaluate: controlling tty path, a session id
   passed explicitly by the caller (e.g. `-n "$CLAUDE_CODE_SESSION_ID"`), refusing outright.

Remote transport: derived names/owners are prefixed with the short hostname
(`laptop.wezterm-17`) so panes on different machines do not collide. Names must not contain
`,` (it parses as `R,C`) or `/` (single path segment).

### Terminal resolvers (implement as many as feasible)

Each resolver returns `(tab number, owner id)` or only `(owner id)` for step 4. Order: most
specific first (tmux before the outer terminal, since e.g. `WEZTERM_PANE` is constant across
tmux panes).

| Terminal | Tab number | Instance id (fallback name) | Status |
|---|---|---|---|
| tmux | `tmux display -p -t $TMUX_PANE '#{window_index}'` | `$TMUX_PANE` | **[unverified]** |
| iTerm2 | `t` field of `$ITERM_SESSION_ID` (`w0t1p0:UUID`) | `w0t1p0` | **[unverified]**: 0- vs 1-based; whether `t` tracks tab reorder/close or is fixed at creation; `wNtN` repeats across windows |
| WezTerm | tab position via `wezterm cli list --format json` | `$WEZTERM_PANE` (verified: monotonic mux-lifetime counter) | CLI output shape **[unverified]** |
| kitty | `kitten @ ls` (needs remote control enabled) | `$KITTY_WINDOW_ID` | **[unverified]**; tab number likely skipped |

The resolver table lives in `internal/termid` so adding a terminal is one entry. Resolvers that
exec a helper bound it with a short timeout and treat failure as "no tab number" (fall to step 4).

### Collisions

Two sessions can map to one slot (tab 13 vs tab 1; `w0t0` vs `w1t0` in iTerm). Policy:
**last writer wins, with an owner tag.**

- `set` carries an optional `owner` string (derived terminal identity, hostname-prefixed when
  remote). The engine's status record stores it; `get` displays it.
- A new write simply takes the key.
- `clear` is conditional: the CLI sends its owner, the daemon blanks/releases only if the recorded
  owner still matches; otherwise a no-op, exit 0. `--force` clears unconditionally. A session
  ending therefore never blanks a key another session has since taken.

This policy is a recommendation not yet explicitly confirmed by the user.

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

## Exit codes (sysexits; none is 2 — Claude Code hooks may treat exit 2 as blocking **[unverified]**)

0 ok · 1 daemon returned an error · 64 usage / no key or device determinable · 66 key not
registered · 69 daemon unreachable · 77 auth missing/rejected · 78 no config/endpoint.

## Files

- `cmd/blincli/`, `internal/client/` (transport resolution, HTTP), `internal/termid/` (resolver
  table), plus the API/engine/dispatcher changes above.
- `make build` also builds `bin/blincli`; both platforms' `install.sh` install `blincli`
  alongside the daemon.
- `integrations/claude/hooks-blincli.json`: the five events of `hooks-basic.json` using
  `blincli set -s claude/{idle,working,waiting} --if-detected` and `blincli clear --if-detected`.
- `integrations/claude/README.md`: blincli section; `hooks-wezterm-pane.json` and its section
  stay as the raw-curl example.
- The pool-conflict note: direct (`idx:`) writes permanently take a key out of the named-claim
  pool, so tab slots 7-12 overlap pool keys (row >= 1) used by `hooks-basic.json` session claims.

## Open items

- Key resolution step 5 (no tab number, no instance id): undecided, see above.
- Owner-tag collision policy: pending explicit confirmation.
- All **[unverified]** entries: verify before implementing the corresponding piece.
