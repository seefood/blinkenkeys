#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
LOG_DIR="$HOME/.local/state/blinkenkeys"
LABEL="com.seefood.blinkenkeysd"
PLIST_DEST="$HOME/Library/LaunchAgents/$LABEL.plist"
DOMAIN_TARGET="gui/$(id -u)/$LABEL"

FORCE=0
if [[ $# -gt 1 ]]; then
	echo "usage: $0 [--force]" >&2
	exit 2
fi
case "${1:-}" in
"") ;;
--force) FORCE=1 ;;
*)
	echo "usage: $0 [--force]" >&2
	exit 2
	;;
esac

bin_changed=0
plist_changed=0

mkdir -p "$BIN_DIR" "$LOG_DIR" "$(dirname "$PLIST_DEST")"

SRC_BIN="$SCRIPT_DIR/../../bin/blinkenkeysd"
if [[ ! -f "$SRC_BIN" ]]; then
	echo "error: $SRC_BIN not found — run 'make build' first" >&2
	exit 1
fi

DEST_BIN="$BIN_DIR/blinkenkeysd"
if [[ "$FORCE" -eq 1 ]] || ! cmp -s "$SRC_BIN" "$DEST_BIN" 2>/dev/null; then
	# install(1) unlinks the destination and creates a fresh inode instead of
	# truncating in place, so this works even while the old binary is running.
	install -m 0755 "$SRC_BIN" "$DEST_BIN"
	bin_changed=1
fi

RENDERED_PLIST="$(mktemp)"
trap 'rm -f "$RENDERED_PLIST"' EXIT
sed -e "s|__BLINKENKEYSD_BIN__|$DEST_BIN|" -e "s|__LOG_DIR__|$LOG_DIR|" \
	"$SCRIPT_DIR/com.seefood.blinkenkeysd.plist" >"$RENDERED_PLIST"

if [[ "$FORCE" -eq 1 ]] || ! cmp -s "$RENDERED_PLIST" "$PLIST_DEST" 2>/dev/null; then
	cp "$RENDERED_PLIST" "$PLIST_DEST"
	plist_changed=1
fi

was_loaded=0
if launchctl print "$DOMAIN_TARGET" >/dev/null 2>&1; then
	was_loaded=1
fi

if [[ "$was_loaded" -eq 0 ]]; then
	launchctl bootstrap "gui/$(id -u)" "$PLIST_DEST"
elif [[ "$FORCE" -eq 1 ]] || [[ "$plist_changed" -eq 1 ]]; then
	launchctl bootout "$DOMAIN_TARGET"
	launchctl bootstrap "gui/$(id -u)" "$PLIST_DEST"
elif [[ "$bin_changed" -eq 1 ]]; then
	launchctl kickstart -k "$DOMAIN_TARGET"
fi

echo
echo "binary: $([[ $bin_changed -eq 1 ]] && echo "installed to $DEST_BIN" || echo "already up to date")"
echo "plist:  $([[ $plist_changed -eq 1 ]] && echo "installed to $PLIST_DEST" || echo "already up to date")"
echo
launchctl print "$DOMAIN_TARGET"
