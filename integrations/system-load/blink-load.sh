#!/usr/bin/env bash
# Polls CPU/RAM load and colors a blinkenkeys key accordingly. Linux-only
# (reads /proc directly) — see README.md in this directory.
set -euo pipefail

SOCKET="${BLINKENKEYS_SOCKET:-$HOME/.local/state/blinkenkeys/api.sock}"
DEVICE="${BLINKENKEYS_DEVICE:?set BLINKENKEYS_DEVICE to your device name - see curl --unix-socket $SOCKET http://localhost/devices}"
KEY="${BLINKENKEYS_KEY:-sysload}"
INTERVAL="${BLINKENKEYS_INTERVAL:-5}"

NPROC=$(nproc)

cpu_load_pct() {
	local load1
	load1=$(cut -d' ' -f1 /proc/loadavg)
	awk -v l="$load1" -v n="$NPROC" 'BEGIN { printf "%d", (l / n) * 100 }'
}

mem_used_pct() {
	awk '
		/^MemTotal:/ { total = $2 }
		/^MemAvailable:/ { avail = $2 }
		END { printf "%d", ((total - avail) / total) * 100 }
	' /proc/meminfo
}

color_for_pct() {
	local pct=$1
	if ((pct >= 80)); then
		echo "#ff0000"
	elif ((pct >= 50)); then
		echo "#ff8800"
	else
		echo "#00ff00"
	fi
}

set_key() {
	curl -s --unix-socket "$SOCKET" \
		-X PUT "http://localhost/devices/$DEVICE/keys/$KEY" \
		-H 'Content-Type: application/json' \
		-d "{\"color\":\"$1\"}" >/dev/null
}

release_key() {
	curl -s --unix-socket "$SOCKET" -X DELETE "http://localhost/devices/$DEVICE/keys/$KEY" >/dev/null || true
}
trap release_key EXIT

while true; do
	cpu=$(cpu_load_pct)
	mem=$(mem_used_pct)
	pct=$((cpu > mem ? cpu : mem))
	set_key "$(color_for_pct "$pct")"
	sleep "$INTERVAL"
done
