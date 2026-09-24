#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
RULES_DEST="/etc/udev/rules.d/99-blinkenkeys.rules"
SERVICE_NAME="blinkenkeysd.service"

FORCE=0
if [[ "${1:-}" == "--force" ]]; then
	FORCE=1
fi

bin_changed=0
unit_changed=0
rule_changed=0

mkdir -p "$BIN_DIR" "$UNIT_DIR"

SRC_BIN="$SCRIPT_DIR/../../bin/blinkenkeysd"
if [[ ! -f "$SRC_BIN" ]]; then
	echo "error: $SRC_BIN not found — run 'make build' first" >&2
	exit 1
fi

DEST_BIN="$BIN_DIR/blinkenkeysd"
if [[ "$FORCE" -eq 1 ]] || ! cmp -s "$SRC_BIN" "$DEST_BIN" 2>/dev/null; then
	cp "$SRC_BIN" "$DEST_BIN"
	chmod +x "$DEST_BIN"
	bin_changed=1
fi

RENDERED_UNIT="$(mktemp)"
trap 'rm -f "$RENDERED_UNIT"' EXIT
sed "s|__BLINKENKEYSD_BIN__|$DEST_BIN|" "$SCRIPT_DIR/blinkenkeysd.service" >"$RENDERED_UNIT"

DEST_UNIT="$UNIT_DIR/$SERVICE_NAME"
if [[ "$FORCE" -eq 1 ]] || ! cmp -s "$RENDERED_UNIT" "$DEST_UNIT" 2>/dev/null; then
	cp "$RENDERED_UNIT" "$DEST_UNIT"
	unit_changed=1
fi

if [[ "$FORCE" -eq 1 ]] || ! cmp -s "$SCRIPT_DIR/99-blinkenkeys.rules" "$RULES_DEST" 2>/dev/null; then
	sudo install -m 0644 "$SCRIPT_DIR/99-blinkenkeys.rules" "$RULES_DEST"
	sudo udevadm control --reload-rules
	sudo udevadm trigger
	rule_changed=1
fi

was_active=0
if systemctl --user is-active --quiet "$SERVICE_NAME"; then
	was_active=1
fi

if [[ "$unit_changed" -eq 1 ]]; then
	systemctl --user daemon-reload
fi

systemctl --user enable --now "$SERVICE_NAME"

if [[ "$was_active" -eq 1 ]] && { [[ "$bin_changed" -eq 1 ]] || [[ "$unit_changed" -eq 1 ]]; }; then
	systemctl --user restart "$SERVICE_NAME"
fi

echo
echo "binary:    $([[ $bin_changed -eq 1 ]] && echo "installed to $DEST_BIN" || echo "already up to date")"
echo "unit:      $([[ $unit_changed -eq 1 ]] && echo "installed to $DEST_UNIT" || echo "already up to date")"
echo "udev rule: $([[ $rule_changed -eq 1 ]] && echo "installed to $RULES_DEST" || echo "already up to date")"
echo
systemctl --user status "$SERVICE_NAME" --no-pager
