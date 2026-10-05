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

## P2

### F10. Client does not decode `layout.pool` / `layout.collision`
- Daemon side done (capabilities `layout` now has `pool`, omitted for the default pool, `[]` when empty, and `collision`). Remaining: client `Capabilities.Layout` in `internal/client` still decodes only `tabs`.

## P3 (tests, polish, docs)

- **T12** `termid`: the second tmux call runs after the first failed; the real-exec test relies on `sleep` being on PATH. Short-circuit, and make the test self-contained.
- **T16** (declined) `RedactURL` masks only the password; the username stays visible (stdlib `url.Redacted`): kept by decision, usernames are not treated as secret.
- **T17** (noted) `clear -n` without `--force` releases the name unconditionally on the first DELETE: kept per the original brief, revisit with F8/F9. Files stay named `set.go` etc. rather than `cmd_set.go`: naming only, do not rename without approval.
- **T21** Tooling: `prek` `go-sec-mod` cannot find `gosec` on PATH, so `make lint` fails on that hook only. Fix the hook entry or document the PATH requirement. `mcp__ide__getDiagnostics` was unavailable during this work, so IDE diagnostics were never run.

## Housekeeping (not code)

- Rename branch `worktree-blincli-001-daemon-and-client` to the `feature/###-phase` convention if desired.
- Remove stale sibling worktrees (`blincli-t4`, `blincli-t8`, `blincli-t9`, `agent-*`) after inspecting them for unmerged work.
- Replace the user's Claude hooks (`~/.claude/settings.json` → dotclaude repo) with `integrations/claude/hooks-blincli.json` equivalents only after installing the new binaries and restarting the daemon (V3, V5).
