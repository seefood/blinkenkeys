#!/usr/bin/env bash
set -euo pipefail

BIN_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
RULES_DEST="/etc/udev/rules.d/99-blinkenkeys.rules"
SERVICE_NAME="blinkenkeysd.service"

DEST_BIN="$BIN_DIR/blinkenkeysd"
DEST_UNIT="$UNIT_DIR/$SERVICE_NAME"

service_removed=0
unit_removed=0
rule_removed=0
bin_removed=0

if systemctl --user is-enabled --quiet "$SERVICE_NAME" 2>/dev/null || systemctl --user is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
	systemctl --user disable --now "$SERVICE_NAME"
	service_removed=1
fi

if [[ -f "$DEST_UNIT" ]]; then
	rm -f "$DEST_UNIT"
	systemctl --user daemon-reload
	unit_removed=1
fi

if [[ -f "$RULES_DEST" ]]; then
	sudo rm -f "$RULES_DEST"
	sudo udevadm control --reload-rules
	sudo udevadm trigger
	rule_removed=1
fi

if [[ -f "$DEST_BIN" ]]; then
	rm -f "$DEST_BIN"
	bin_removed=1
fi

echo
echo "service:   $([[ $service_removed -eq 1 ]] && echo "disabled and stopped" || echo "was not loaded")"
echo "unit:      $([[ $unit_removed -eq 1 ]] && echo "removed ($DEST_UNIT)" || echo "already absent")"
echo "udev rule: $([[ $rule_removed -eq 1 ]] && echo "removed ($RULES_DEST)" || echo "already absent")"
echo "binary:    $([[ $bin_removed -eq 1 ]] && echo "removed ($DEST_BIN)" || echo "already absent")"
