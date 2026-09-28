#!/bin/sh
# Install (or upgrade) probe-agent as a systemd service on a Linux host.
#
# One-liner, downloading the binary from your own dashboard:
#   curl -fsSL https://probe.example.com/install-agent.sh | sudo \
#     PROBE_SERVER=https://probe.example.com PROBE_TOKEN=xxx \
#     PROBE_NAME=home-sz PROBE_LOCATION="广东 深圳" PROBE_ISP=电信 sh
#
# Or with a local binary:
#   sudo PROBE_SERVER=... PROBE_TOKEN=... ./install-agent.sh ./probe-agent-linux-arm64
#
# The binary lives in /var/lib/probe-agent (owned by a dedicated probe-agent
# user) so the agent can update itself when the dashboard is upgraded.
# Re-running this script is safe.
set -eu

: "${PROBE_SERVER:?PROBE_SERVER is required}"
: "${PROBE_TOKEN:?PROBE_TOKEN is required}"
PROBE_NAME="${PROBE_NAME:-$(hostname)}"
BIN_DIR=/var/lib/probe-agent
BIN_SRC="${1:-}"

if [ "$(id -u)" != 0 ]; then
  echo "run as root (sudo)" >&2; exit 1
fi
if [ "$(uname -s)" != Linux ]; then
  echo "this installer is for Linux + systemd" >&2; exit 1
fi

cleanup() { [ -n "${TMP_BIN:-}" ] && rm -f "$TMP_BIN"; }
trap cleanup EXIT

if [ -z "$BIN_SRC" ]; then
  case "$(uname -m)" in
    x86_64|amd64)   key=linux-amd64 ;;
    aarch64|arm64)  key=linux-arm64 ;;
    armv7l|armv8l)  key=linux-armv7 ;;
    armv6l)         key=linux-armv6 ;;
    i?86)           key=linux-386 ;;
    riscv64)        key=linux-riscv64 ;;
    mips64el)       key=linux-mips64le ;;
    *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
  command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
  TMP_BIN=$(mktemp)
  echo "downloading $key from $PROBE_SERVER ..."
  curl -fsSL -H "Authorization: Bearer $PROBE_TOKEN" "${PROBE_SERVER%/}/api/agent/download/$key" -o "$TMP_BIN"
  BIN_SRC="$TMP_BIN"
elif [ ! -f "$BIN_SRC" ]; then
  echo "binary not found: $BIN_SRC" >&2; exit 2
fi
chmod 755 "$BIN_SRC"
"$BIN_SRC" version >/dev/null || { echo "downloaded binary does not run on this host" >&2; exit 1; }

# Dedicated unprivileged system user. (DynamicUser= is deliberately not used:
# it moves the state dir under /var/lib/private, which the service user cannot
# traverse to exec the binary.)
SVC_USER=probe-agent
if ! id -u "$SVC_USER" >/dev/null 2>&1; then
  useradd --system --user-group --home-dir "$BIN_DIR" --no-create-home --shell /usr/sbin/nologin "$SVC_USER" 2>/dev/null \
    || adduser --system --group --home "$BIN_DIR" --no-create-home --shell /usr/sbin/nologin "$SVC_USER" 2>/dev/null \
    || { addgroup -S "$SVC_USER" 2>/dev/null; adduser -S -D -H -h "$BIN_DIR" -s /sbin/nologin -G "$SVC_USER" "$SVC_USER"; } \
    || { echo "could not create user $SVC_USER" >&2; exit 1; }
fi

systemctl stop probe-agent 2>/dev/null || true
# Undo the DynamicUser= private-dir layout written by an earlier version of this script.
if [ -L "$BIN_DIR" ]; then
  rm -f "$BIN_DIR"
  [ -d /var/lib/private/probe-agent ] && mv /var/lib/private/probe-agent "$BIN_DIR"
fi
install -d -m 0755 "$BIN_DIR"
install -m 0755 "$BIN_SRC" "$BIN_DIR/probe-agent"
rm -f "$BIN_DIR"/.probe-agent-update-* 2>/dev/null || true
chown -R "$SVC_USER:$SVC_USER" "$BIN_DIR"
# Legacy location from earlier installs.
[ -f /usr/local/bin/probe-agent ] && rm -f /usr/local/bin/probe-agent

umask 077
cat > /etc/probe-agent.env <<ENV
PROBE_SERVER=$PROBE_SERVER
PROBE_TOKEN=$PROBE_TOKEN
PROBE_NAME=$PROBE_NAME
PROBE_LOCATION=${PROBE_LOCATION:-}
PROBE_ISP=${PROBE_ISP:-}
PROBE_TAGS=${PROBE_TAGS:-}
ENV
chmod 600 /etc/probe-agent.env

cat > /etc/systemd/system/probe-agent.service <<'UNIT'
[Unit]
Description=probe-platform agent
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=/etc/probe-agent.env
User=probe-agent
Group=probe-agent
# Binary lives in the service user's writable state dir so self-update can replace it.
StateDirectory=probe-agent
ExecStart=/var/lib/probe-agent/probe-agent
Restart=always
RestartSec=5
AmbientCapabilities=CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_RAW
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now probe-agent >/dev/null 2>&1 || true
systemctl restart probe-agent
sleep 2
systemctl --no-pager --lines=0 status probe-agent || true
echo
echo "installed $("$BIN_DIR/probe-agent" version). logs: journalctl -u probe-agent -f"
