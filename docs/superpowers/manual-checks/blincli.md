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
