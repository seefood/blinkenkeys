# TCP listener manual check

Manual, mutates real config/service state (`~/.config/blinkenkeys/config.yaml`,
the running `blinkenkeysd` service) — not run in CI. Run after any change to
`listeners.tcp` handling (`cmd/blinkenkeysd/main.go`'s `newTCPServer`,
`internal/api/auth.go`, or `internal/config`'s TCP schema/validation).

Prerequisites: `blinkenkeysd` installed and running (see
`phase2-5-linux-install.md`), with at least one device already enumerable
over the Unix socket so `/devices` has something to return.

1. **Baseline: TCP disabled by default.** With a fresh/unedited
   `config.yaml` (no `listeners.tcp` block), confirm nothing is listening:
   ```bash
   ss -ltnp 2>/dev/null | grep 49994 || echo "not listening (expected)"
   ```

2. **Enable TCP in config:** edit
   `${XDG_CONFIG_HOME:-~/.config}/blinkenkeys/config.yaml`, uncommenting/adding:
   ```yaml
   listeners:
     tcp:
       address: ":49994"
       token: "<pick your own token, don't reuse this doc's example>"
   ```
   Restart the service so it's picked up:
   ```bash
   systemctl --user restart blinkenkeysd.service
   systemctl --user status blinkenkeysd.service 2>&1 | head -3
   ```
   Confirm: `active (running)`, and the log shows the TCP listen line:
   ```bash
   journalctl --user -u blinkenkeysd.service --since "10 seconds ago" --no-pager | grep tcp
   ```
   Expect a line like `blinkenkeysd listening tcp=:49994`.

3. **Config validation: TCP block present without a token is rejected.**
   Temporarily remove the `token:` line (keep `address:`), restart, and
   confirm the service fails to start rather than silently serving
   unauthenticated:
   ```bash
   systemctl --user restart blinkenkeysd.service
   systemctl --user status blinkenkeysd.service 2>&1 | head -3
   journalctl --user -u blinkenkeysd.service --since "10 seconds ago" --no-pager | tail -5
   ```
   Expect the service in a failed state and a config-validation error in the
   log. Restore the `token:` line and restart again before continuing.

4. **Missing bearer token → 401:**
   ```bash
   curl -s -o /dev/null -w '%{http_code}\n' http://localhost:49994/devices
   ```
   Expect `401`.

5. **Wrong bearer token → 403:**
   ```bash
   curl -s -o /dev/null -w '%{http_code}\n' \
     -H "Authorization: Bearer wrong-token" http://localhost:49994/devices
   ```
   Expect `403`.

6. **Correct bearer token → 200, same data as the Unix socket:**
   ```bash
   curl -s -H "Authorization: Bearer <your token from step 2>" \
     http://localhost:49994/devices
   echo ---
   curl -s --unix-socket ~/.local/state/blinkenkeys/api.sock http://localhost/devices
   ```
   Confirm both calls return `200` and the same device list.

7. **Unix socket unaffected:** confirm the socket still requires no token
   (filesystem permissions are its access control, per
   `internal/api/auth.go`'s `RequireToken` doc comment) and still works:
   ```bash
   curl -s -o /dev/null -w '%{http_code}\n' \
     --unix-socket ~/.local/state/blinkenkeys/api.sock http://localhost/devices
   ```
   Expect `200` with no `Authorization` header sent.

8. **Disable TCP again:** remove/comment out the `listeners.tcp` block,
   restart, and confirm step 1's baseline holds again (`ss` shows nothing on
   the port, socket access still works).

9. **Remote reachability (optional, needs a second machine on the same
   LAN/VPN):** from another host, confirm the port is reachable at the
   daemon machine's LAN address (not just `localhost`) when TCP is enabled,
   and confirm firewall rules block it as expected when it should be
   closed. Skip this step on a single-machine dev setup.
