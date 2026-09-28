#!/bin/sh
# Install probe-agent as a systemd service on a Linux host.
#
#   sudo PROBE_SERVER=https://probe.example.com PROBE_TOKEN=xxx \
#        PROBE_NAME=home-sz PROBE_LOCATION="广东 深圳" PROBE_ISP=电信 \
#        ./install-agent.sh ./probe-agent-linux-arm64
#
# Re-running updates the binary and the env file, then restarts the service.
set -eu

BIN_SRC="${1:-}"
if [ -z "$BIN_SRC" ] || [ ! -f "$BIN_SRC" ]; then
  echo "usage: $0 <path-to-probe-agent-binary>" >&2; exit 2
fi
: "${PROBE_SERVER:?PROBE_SERVER is required}"
: "${PROBE_TOKEN:?PROBE_TOKEN is required}"
PROBE_NAME="${PROBE_NAME:-$(hostname)}"

if [ "$(id -u)" != 0 ]; then
  echo "run as root (sudo -E $0 ...)" >&2; exit 1
fi

install -m 0755 "$BIN_SRC" /usr/local/bin/probe-agent
if command -v setcap >/dev/null 2>&1; then
  setcap cap_net_raw+ep /usr/local/bin/probe-agent || echo "warn: setcap failed; ping/mtr will rely on AmbientCapabilities" >&2
fi

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
ExecStart=/usr/local/bin/probe-agent
Restart=always
RestartSec=5
DynamicUser=yes
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
systemctl enable --now probe-agent
systemctl restart probe-agent
sleep 2
systemctl --no-pager --lines=0 status probe-agent || true
echo
echo "installed. logs: journalctl -u probe-agent -f"
