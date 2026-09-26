#!/usr/bin/env bash
set -euo pipefail

BIN_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
LABEL="com.seefood.blinkenkeysd"
PLIST_DEST="$HOME/Library/LaunchAgents/$LABEL.plist"
DOMAIN_TARGET="gui/$(id -u)/$LABEL"

if [[ $# -ne 0 ]]; then
	echo "usage: $0" >&2
	exit 2
fi

DEST_BIN="$BIN_DIR/blinkenkeysd"

agent_removed=0
plist_removed=0
bin_removed=0

if launchctl print "$DOMAIN_TARGET" >/dev/null 2>&1; then
	launchctl bootout "$DOMAIN_TARGET"
	agent_removed=1
fi

if [[ -f "$PLIST_DEST" ]]; then
	rm -f "$PLIST_DEST"
	plist_removed=1
fi

if [[ -f "$DEST_BIN" ]]; then
	rm -f "$DEST_BIN"
	bin_removed=1
fi

echo
echo "agent:  $([[ $agent_removed -eq 1 ]] && echo "unloaded" || echo "was not loaded")"
echo "plist:  $([[ $plist_removed -eq 1 ]] && echo "removed ($PLIST_DEST)" || echo "already absent")"
echo "binary: $([[ $bin_removed -eq 1 ]] && echo "removed ($DEST_BIN)" || echo "already absent")"
