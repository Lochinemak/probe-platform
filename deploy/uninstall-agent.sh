#!/bin/sh
# Uninstall probe-agent from a Linux host (systemd) or an OpenWrt / iStoreOS
# router (procd). Whatever install-agent.sh / install-agent-openwrt.sh put on
# the box is detected and removed: the service, the binary (plus self-update
# leftovers), /etc/probe-agent.env and the probe-agent system user. Running it
# on a host that has no agent is harmless, and so is running it twice.
#
#   Linux:    curl -fsSL https://probe.example.com/uninstall-agent.sh | sudo sh
#   OpenWrt:  curl -fsSL https://probe.example.com/uninstall-agent.sh | sh
#
#   Keep /etc/probe-agent.env (and/or the service user) for a later reinstall:
#             curl -fsSL https://probe.example.com/uninstall-agent.sh | sudo sh -s -- --keep-config --keep-user
#
# Docker nodes are not touched: `docker rm -f probe-agent` (NAS: delete the
# project in Container Manager / Container Station). The node keeps showing as
# offline on the dashboard until you delete it on the Agents page.
set -eu

SVC=probe-agent
SVC_USER=probe-agent
BIN_DIR=/var/lib/probe-agent
ENV_FILE=/etc/probe-agent.env
KEEP_CONFIG="${KEEP_CONFIG:-0}"
KEEP_USER="${KEEP_USER:-0}"

usage() {
  cat <<'EOF'
usage: uninstall-agent.sh [--keep-config] [--keep-user]
  --keep-config   keep /etc/probe-agent.env (a later install-agent.sh run overwrites it anyway)
  --keep-user     keep the probe-agent system user and group
KEEP_CONFIG=1 / KEEP_USER=1 in the environment do the same.
EOF
}
for arg in "$@"; do
  case "$arg" in
    --keep-config) KEEP_CONFIG=1 ;;
    --keep-user)   KEEP_USER=1 ;;
    -h|--help)     usage; exit 0 ;;
    *) echo "unknown option: $arg" >&2; usage >&2; exit 2 ;;
  esac
done

[ "$(id -u)" = 0 ] || { echo "run as root (sudo)" >&2; exit 1; }

removed=0
note() { echo "  - $1"; removed=$((removed + 1)); }

echo "uninstalling probe-agent ..."

# --- systemd service (install-agent.sh) ---
if command -v systemctl >/dev/null 2>&1 && systemctl cat "$SVC" >/dev/null 2>&1; then
  systemctl disable --now "$SVC" >/dev/null 2>&1 || true
  systemctl reset-failed "$SVC" >/dev/null 2>&1 || true
  note "stopped and disabled systemd service $SVC"
fi
for unit in /etc/systemd/system/$SVC.service /lib/systemd/system/$SVC.service /usr/lib/systemd/system/$SVC.service; do
  if [ -f "$unit" ] || [ -L "$unit" ]; then rm -f "$unit"; note "removed $unit"; fi
done
if [ -d /etc/systemd/system/$SVC.service.d ]; then
  rm -rf /etc/systemd/system/$SVC.service.d; note "removed /etc/systemd/system/$SVC.service.d"
fi
if command -v systemctl >/dev/null 2>&1; then systemctl daemon-reload >/dev/null 2>&1 || true; fi

# --- procd service (install-agent-openwrt.sh) ---
if [ -f /etc/init.d/$SVC ]; then
  /etc/init.d/$SVC stop >/dev/null 2>&1 || true
  /etc/init.d/$SVC disable >/dev/null 2>&1 || true
  rm -f /etc/init.d/$SVC /etc/rc.d/S??$SVC /etc/rc.d/K??$SVC
  note "stopped and removed procd service /etc/init.d/$SVC"
fi

# --- stray processes started from one of the known install paths (an agent
#     running inside a Docker container on this host is left alone) ---
if command -v pkill >/dev/null 2>&1; then
  if pkill -f "^(/var/lib/probe-agent|/usr/bin|/usr/local/bin)/probe-agent( |$)" 2>/dev/null; then
    sleep 1; note "killed leftover probe-agent process(es)"
  fi
fi

# --- binary, self-update leftovers (.prev, .probe-agent-update-*), legacy locations ---
if [ -L "$BIN_DIR" ] || [ -d "$BIN_DIR" ]; then rm -rf "$BIN_DIR"; note "removed $BIN_DIR"; fi
if [ -d /var/lib/private/$SVC ]; then rm -rf /var/lib/private/$SVC; note "removed /var/lib/private/$SVC (legacy DynamicUser layout)"; fi
for f in /usr/bin/$SVC /usr/bin/$SVC.new /usr/bin/$SVC.prev /usr/local/bin/$SVC /usr/local/bin/$SVC.prev; do
  if [ -f "$f" ]; then rm -f "$f"; note "removed $f"; fi
done

# --- config ---
if [ -f "$ENV_FILE" ]; then
  if [ "$KEEP_CONFIG" = 1 ]; then echo "  - kept $ENV_FILE"; else rm -f "$ENV_FILE"; note "removed $ENV_FILE"; fi
fi

# --- service user (Linux installs only; OpenWrt runs the agent as root) ---
if id -u "$SVC_USER" >/dev/null 2>&1; then
  if [ "$KEEP_USER" = 1 ]; then
    echo "  - kept user $SVC_USER"
  elif userdel "$SVC_USER" 2>/dev/null || deluser "$SVC_USER" 2>/dev/null; then
    note "removed user $SVC_USER"
  else
    echo "warning: could not remove user $SVC_USER; remove it by hand: userdel $SVC_USER" >&2
  fi
fi
if [ "$KEEP_USER" != 1 ] && grep -q "^$SVC_USER:" /etc/group 2>/dev/null; then
  groupdel "$SVC_USER" 2>/dev/null || delgroup "$SVC_USER" 2>/dev/null || true
  if ! grep -q "^$SVC_USER:" /etc/group 2>/dev/null; then note "removed group $SVC_USER"; fi
fi

echo
if [ "$removed" = 0 ]; then
  echo "nothing to do: probe-agent is not installed on this host."
else
  echo "probe-agent uninstalled."
fi
echo "the node stays listed (offline) on the dashboard until you delete it on the Agents page."
