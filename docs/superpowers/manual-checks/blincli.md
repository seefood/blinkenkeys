# blincli / daemon read-API manual check

Requires a running `bin/blinkenkeysd` with a device attached and, in `config.yaml`:

```yaml
devices:
  - id: <your device name>
    keys: {tabs: [0-5], pool: [6-11]}
```

Set `S=$HOME/.local/state/blinkenkeys/api.sock` and `D=<device name>`.

## Daemon half

1. `curl -s --unix-socket $S http://localhost/devices/$D | jq .layout` → `{"tabs":[0,1,2,3,4,5]}`.
2. `curl -s --unix-socket $S -X PUT http://localhost/devices/$D/keys/idx:0 -d '{"state":"claude/working","owner":"manual"}'` → 204; key 0 breathes orange.
3. `curl -s --unix-socket $S http://localhost/devices/$D/keys/idx:0 | jq` → `kind:"direct"`, `source.type:"state"`, `source.owner:"manual"`, `effect.running:true`, `color` moving between polls.
4. `curl -s --unix-socket $S http://localhost/devices/$D/keys/neverclaimed -w '%{http_code}\n'` → 404, and a following `GET /devices/$D/keys` does **not** list it (GET must not claim).
5. `curl -s --unix-socket $S -X DELETE "http://localhost/devices/$D/keys/idx:0?owner=someone-else" -w '%{http_code}\n'` → 204 and key 0 **stays lit**.
6. Same with `?owner=manual` → 204 and the key goes dark; `GET` of it → 404-or-blank-state per the engine record being dropped (it is dropped: 404 only if also not direct-owned; direct ownership persists, so expect 200 with no `source`).
7. `curl ... -X PUT .../keys/named1 -d '{"color":"red"}'` → lands on idx 6+ (pool), not 0–5.
8. Collision, `last-wins` (default): `PUT .../keys/idx:6 -d '{"color":"blue"}'` → key 6 turns blue; `GET .../keys/named1` → 404 (claim released); a new name claims idx 7.
9. Collision, `displace`: add `collision: displace` under `keys:`, restart, `PUT .../keys/named1 {"color":"red"}` (lands on idx 6), then `PUT .../keys/idx:6 {"color":"blue"}` → key 6 blue, `GET .../keys/named1` → `idx` 7 and key 7 red (a running effect restarts there). Fill the pool (6–11) first and repeat: key blue, `named1` gone, daemon log has a `displace: pool full` warning.

## Client half

Build and install: `make build && packaging/linux/install.sh` (or macOS).

1. `blincli detect` in each terminal you use → correct terminal, tab, key; run it in a plain `ssh` shell → "none recognized" and key `none (…)` unless `$CLAUDE_CODE_SESSION_ID`/`$BLINKENKEYS_NAME` is set.
2. **iTerm2**: open tabs 1–3; in each run `blincli set -c red`; keys `idx:0/1/2` light red, matching Option-1/2/3. Reorder tabs, run again: the key follows the *original* tab number (the known staleness limitation) — record the observed behaviour here.
3. **WezTerm** (verifies the unverified list-order assumption): open 3 tabs, drag tab 3 to position 1, run `blincli detect` in it → expect tab 1. If it reports 3, `wezterm cli list` is not in tab order; fix `weztermTab` (`internal/termid/termid.go`) to order by `window_id`'s tab list another way, update the spec table, and note it here.
4. **tmux**: with `base-index 1`, window 2 → `blincli detect` reports tab 2.
5. `blincli set -s claude/working` then `blincli get` → source, effect running, color, age. `blincli get -a` lists it. `blincli get -k idx:5` on a never-written key → exit 66.
6. Collision: tab 7 (or `-m 4` in tab 5) maps onto an occupied slot: the key is taken over; `blincli clear` from the *first* tab afterwards leaves it lit (owner mismatch), `clear --force` blanks it.
7. Named fallback: `env -u ITERM_SESSION_ID -u WEZTERM_PANE -u TMUX_PANE CLAUDE_CODE_SESSION_ID=abc blincli set -c blue` → lands on idx 6+ (pool). With `pool: []` in the daemon config → lands on a tab slot via hash (`-v` shows "sharing slot").
8. Exit codes: stop the daemon → `blincli set …` exits 69; wrong `--token` against a TCP listener → 77; remove the config and socket → 78 with the setup help; `blincli set` with no action → 64.
9. Hooks: merge `integrations/claude/hooks-blincli.json` into `.claude/settings.local.json`, run a Claude Code session: LED goes idle → working → waiting → dark on exit.
10. Out-of-range layout entries (deliberate non-validation, see the spec): a `pool` entry beyond the device's key count is skipped at claim time; `blincli set -c red -k idx:99` and a tab slot beyond the key count → 404 from the daemon.

### Results (2026-10-05, agent session without keyboard or terminals)

The agent environment is Linux with no VialRGB keyboard attached and no iTerm2/WezTerm/tmux sessions to drive. **Every numbered check above is *not run*** (1–10), including the iTerm2 reorder-staleness observation (2), the WezTerm tab-order verification (3), and the live Claude Code hooks session (9). No observed results are recorded; the WezTerm tab ordering remains **[unverified]** in the spec. Automated coverage (`make test`) exercises the same logic against fakes.
