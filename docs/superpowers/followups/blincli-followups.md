# blincli follow-ups

Open work left after the blincli implementation
(`docs/superpowers/plans/2026-10-05-blincli-implementation.md`, spec
`docs/superpowers/specs/2026-10-05-blincli-design.md`). Each item is
self-contained so an agent can pick it up cold.

Conventions for every item: strict TDD (write the test, watch it fail, fix,
watch it pass, run `nice make test`); stdlib `flag` only, no new deps;
`cmd/blincli`, `internal/client`, `internal/termid` must never import
`internal/hid|dispatcher|effects|api` (check with
`go list -deps ./cmd/blincli | grep -E 'internal/(hid|dispatcher|effects|api)|go-hid'`);
exit codes are sysexits and never 2; run gosec by full path
(`~/go/bin/gosec -quiet ./<pkg>/...`) and gofmt on touched files. Tests must
never touch the real daemon socket: use httptest / temp sockets and
`t.Setenv` for `HOME`, `XDG_*`, `BLINKENKEYS_*`.

Priority: **P1** behavior/security gap, **P2** correctness edge, **P3** polish/tests/docs.

## Not yet verified (needs hardware or a real install)

| ID | What | How to close |
|----|------|--------------|
| V1 | All 10 checks in `docs/superpowers/manual-checks/blincli.md` are "not run". | Run them with real iTerm2 / WezTerm / tmux and a keyboard; record observed results in that file. |
| V2 | WezTerm: `wezterm cli list` order equals tab order is an unverified assumption (`weztermTab` in `internal/termid/termid.go`). | Manual check 3. If wrong, order by the window's tab list another way, update the spec table. |
| V3 | Installers (`packaging/{linux,macos}/{install,uninstall}.sh`) were only read, shellchecked and shfmt'd, never executed (they need sudo/systemctl/launchctl). | Run on a throwaway Linux box/VM and a Mac; confirm the `cli:` summary lines, that a CLI-only change never restarts the daemon. |
| V4 | Kitty, zellij, GNU screen and Windows Terminal env detection are unverified (spec marks them so). | Verify the env vars on each terminal, add resolver tests. |
| V5 | The running daemon on the dev machine predates this branch (`GET /keys/{pos}` → 405). | Install the branch's `blinkenkeysd` and restart before using `hooks-blincli.json`. |

## P1

### F1. Malformed `blincli.yaml` should exit 78, not 1
- Where: `internal/client/config.go` (parse error path), exit mapping in `cmd/blincli/main.go`.
- Today: a YAML parse error returns a plain error → exit 1 ("daemon error"). Spec: 78 = no/invalid config.
- Test: write an invalid file, assert exit 78 and that the message contains no file content (token redaction already covered by `TestConfigParseErrorDoesNotLeakToken`-style tests).

### F2. Warn when `blincli.yaml` holding a token is group/world-readable
- Where: `internal/client/config.go` `LoadFile`.
- Today: `config init` writes 0600 but nothing checks a hand-edited or old file.
- Fix: if the file contains a token and mode & 0o077 != 0, print a one-line warning on stderr (not an error; keep exit codes). Skip on platforms without Unix modes.
- Test: 0644 file with token → warning, no token in output; 0600 → silent.

### F3. A runtime panic exits 2
- Where: `cmd/blincli/main.go` `main`. Claude Code hooks treat exit 2 as blocking.
- Fix: `defer` a recover that prints a short error and exits 1.
- Test: inject a panicking command via the registry, assert exit 1 and not 2.

## P2

### F4. `-n NAME` validation
- Where: `internal/client/key.go` (~L271), `termid.Sanitize`.
- Today: `-n led:3` / `-n idx:2` is treated by the daemon as a direct address (a write, not a claim); `-n .` / `-n ..` hit ServeMux path-cleaning redirects.
- Fix: reject those (usage error 64) or run `Sanitize` on explicit names.
- Test: each of the four inputs exits 64 (or is sanitized) and sends no request.

### F5. Overlong names fail every write with a 400
- Today: daemon owner/name limit is 1–128 bytes; sanitized name + host prefix (and `$BLINKENKEYS_NAME`) is unbounded, so a long name makes every write exit 1.
- Fix: truncate with a stable hash suffix in `Sanitize`/`Qualify` so the result is ≤128 bytes and deterministic.
- Test: 300-byte input yields ≤128 bytes, same input → same output, distinct inputs → distinct outputs.

### F6. `-n NAME` is host-prefixed on remote endpoints
- Where: `Plan.Qualify` applied to `ModeNamed` from `-n`. Spec only prefixes *derived* names.
- Effect: two hosts cannot intentionally share an explicit name.
- Fix: qualify only derived names; keep explicit `-n` verbatim. Update `get` to match (it must use the same rule as set/clear).
- Test: remote `set -n foo` PUTs `foo`; derived names still get the host prefix.

### F7. `--url` with path or query is concatenated raw
- Where: `internal/client/http.go` (~L79): `http://h:1/?x=1` → `…/?x=1/devices/…`.
- Fix: reject a non-empty path (other than `/`) or query in `Endpoint` resolution with usage error 64, or join with `url.JoinPath`.
- Test: both forms.

### F8. Conditional `clear` after a daemon restart
- Where: `internal/api/handlers.go` (~L212-216). Status records are in-memory, so after a restart a stale session's `clear?owner=` finds no record and blanks the key now owned by another session.
- Options: persist nothing and document (current), or have the daemon refuse conditional clears with no record (409/no-op). Needs a spec decision first; write the decision into the spec, then implement test-first.

### F9. Non-atomic check-then-clear
- Where: `internal/api/handlers.go` conditional DELETE (Status, then SetColor). A racing owner write inside that window is blanked.
- Fix: add `effects.Engine.ClearIfOwner(key, owner)` doing the check and blank under the engine lock; use it from the handler.
- Test: concurrent PUT-by-other-owner / DELETE-by-first-owner under `-race`.

### F10. `GET /devices/{name}` layout exposes `tabs` only
- Where: `internal/api/keyview.go:16-21`. Spec says `layout: {tabs, pool}`.
- Fix: add `pool` (and `collision`), update the client `Capabilities.Layout`, CHANGELOG and README wording.
- Test: configured pool/collision round-trips; omitted vs `pool: []` stay distinguishable.

### F11. `GET /keys` lists only keys with status records
- Where: `internal/api/keyview.go:132` (`listKeys`). A claimed or direct-owned key without a record (e.g. after an internal blank) is missing; the spec says "every registered key".
- Fix: enumerate the registry, join with status records.
- Test: claim a key, blank it internally, assert it is still listed.

### F12. Failed displaced-claim carry replay is only logged
- Where: `internal/dispatcher/place.go` and `internal/api/handlers.go` replay. Best-effort race between `Place` and the replay is documented but the user sees nothing.
- Fix: surface the failure (log level, response header, or status record flag); decide with spec owner.

## P3 (tests, polish, docs)

- **T1** `KeyList` null YAML (`pool: ~`) behavior: decide (no pool vs default pool), document, test. `config/keylist.go`.
- **T2** Parse errors double-prefix `config:` when wrapped by `Load`. Fix prefixing in one place.
- **T3** `TestKeyListOmittedStaysNil` passes vacuously if unmarshal errors: assert `err == nil` first.
- **T4** `SetLayout`/`Layout` share `Tabs`/`Pool` slices without copying: copy on set and on read.
- **T5** Add test: `SetCaps` resolving pending named writes under a layout.
- **T6** Add test: `Lookup` leaves the claim's idle clock unchanged; test unknown device and `KeyInfo` on a named claim.
- **T7** `Lookup` on a non-name address may trigger capabilities HID I/O (`opGetCapabilities`). Document or guard.
- **T8** `HSV.Hex` tests cover only S=0/V=0/H=0: add hue regions 1–5 as a table test.
- **T9** A dropped Tick on write failure reads as a normal finish in effect status records: add a failure marker; test that a failed write leaves the old record intact.
- **T10** Key view reads (`getKey`, `buildView`) are non-atomic and `getKey` calls `KeyInfo` twice (`keyview.go:116`): single read.
- **T11** `Place` has no live-dispatcher test (fakes only): add one against the real dispatcher with a fake HID backend.
- **T12** `termid`: the second tmux call runs after the first failed; the real-exec test relies on `sleep` being on PATH. Short-circuit, and make the test self-contained.
- **T13** Token-file read error: wrap with `%w` so the cause is inspectable (check the final text still has no token).
- **T14** `detect` ignores `-v`/`-q`; `detect -h` prints the global usage; `vlog` uses a non-constant format string.
- **T15** `-v` given before the command can only be switched off after it with `-v=false`: document, or make command-position tri-state.
- **T16** `RedactURL` masks only the password; the username stays visible (stdlib `url.Redacted`). Mask the username too if usernames are considered sensitive.
- **T17** `set`/`get`: redundant `slots<0` clamp; `clear -n` without `--force` releases the name unconditionally on the first DELETE (per brief; revisit with F8/F9); files are named `set.go` etc. rather than `cmd_set.go` (naming only, do not rename without approval).
- **T18** `-m N` semantics (idx 0..N-1, same as set) changed during a fix round with no dedicated test: add one. `planKey` runs `Detect` even for `-k`: skip when a key is explicit.
- **T19** `writeConfig` (`cmd/blincli/config.go`): `f.Sync()` before rename; no-clobber create without `--force` (`os.Link` + remove temp); tests for `--force` on a directory target and for no leftover `.blincli-*.tmp` after failure.
- **T20** Docs: `examples/config/README.md` comment indent in the schema block; manual-check step 6 wording is hedged ("404-or-blank-state"): make it a single expected result; integrations README: add a WezTerm tab-order caveat, mention the full-pool `displace` fallback and that `applyPending` always uses last-wins; manual-check "automated coverage exercises the same logic" sentence is unverified, reword or remove; check 10's `-k idx:99 → 404` is inferred, mark "expected".
- **T21** Tooling: `prek` `go-sec-mod` cannot find `gosec` on PATH, so `make lint` fails on that hook only. Fix the hook entry or document the PATH requirement. `mcp__ide__getDiagnostics` was unavailable during this work, so IDE diagnostics were never run.

## Housekeeping (not code)

- Rename branch `worktree-blincli-001-daemon-and-client` to the `feature/###-phase` convention if desired.
- Remove stale sibling worktrees (`blincli-t4`, `blincli-t8`, `blincli-t9`, `agent-*`) after inspecting them for unmerged work.
- Replace the user's Claude hooks (`~/.claude/settings.json` → dotclaude repo) with `integrations/claude/hooks-blincli.json` equivalents only after installing the new binaries and restarting the daemon (V3, V5).
