#!/bin/sh
# Install (or upgrade) probe-agent on OpenWrt / iStoreOS (procd, no systemd).
#
#   curl -fsSL https://probe.example.com/install-agent-openwrt.sh | \
#     PROBE_SERVER=https://probe.example.com PROBE_TOKEN=xxx \
#     PROBE_NAME=home-router PROBE_LOCATION="广东 深圳" PROBE_ISP=电信 sh
#
# Needs ~8 MB of overlay space and ~10 MB RAM. Runs as root (routers have no
# unprivileged service users), so raw ICMP for ping/mtr just works. The
# binary lives on the writable overlay so self-update can replace it.
set -eu

: "${PROBE_SERVER:?PROBE_SERVER is required}"
: "${PROBE_TOKEN:?PROBE_TOKEN is required}"
PROBE_NAME="${PROBE_NAME:-$(uci -q get system.@system[0].hostname || hostname)}"
BIN=/usr/bin/probe-agent
BIN_SRC="${1:-}"

[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
[ -f /etc/openwrt_release ] || echo "warning: this does not look like OpenWrt" >&2

fetch() { # url dest
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -H "Authorization: Bearer $PROBE_TOKEN" "$1" -o "$2"
  else
    wget -q --header="Authorization: Bearer $PROBE_TOKEN" -O "$2" "$1"
  fi
}

cleanup() { [ -n "${TMP_BIN:-}" ] && rm -f "$TMP_BIN"; }
trap cleanup EXIT

if [ -z "$BIN_SRC" ]; then
  case "$(uname -m)" in
    x86_64)         key=linux-amd64 ;;
    aarch64|arm64)  key=linux-arm64 ;;   # MT7981 / MT7986 (Filogic), RK3568, ...
    armv7l|armv8l)  key=linux-armv7 ;;
    armv6l)         key=linux-armv6 ;;
    i?86)           key=linux-386 ;;
    riscv64)        key=linux-riscv64 ;;
    mips64el)       key=linux-mips64le ;;
    mips|mipsel)    # MT7621 and friends: 32-bit MIPS, endianness from the ELF header of /bin/sh
                    if [ "$(dd if=/bin/sh bs=1 skip=5 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')" = 01 ]; then key=linux-mipsle; else key=linux-mips; fi ;;
    *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
  TMP_BIN=$(mktemp)
  echo "downloading $key from $PROBE_SERVER ..."
  fetch "${PROBE_SERVER%/}/api/agent/download/$key" "$TMP_BIN"
  BIN_SRC="$TMP_BIN"
elif [ ! -f "$BIN_SRC" ]; then
  echo "binary not found: $BIN_SRC" >&2; exit 2
fi
chmod 755 "$BIN_SRC"
"$BIN_SRC" version >/dev/null || { echo "binary does not run on this device" >&2; exit 1; }

/etc/init.d/probe-agent stop 2>/dev/null || true
cp "$BIN_SRC" "$BIN.new" && mv -f "$BIN.new" "$BIN"

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

cat > /etc/init.d/probe-agent <<'INIT'
#!/bin/sh /etc/rc.common
# probe-platform agent (procd)
USE_PROCD=1
START=95
STOP=10

start_service() {
	[ -f /etc/probe-agent.env ] || { echo "/etc/probe-agent.env missing"; return 1; }
	. /etc/probe-agent.env
	procd_open_instance probe-agent
	procd_set_param command /usr/bin/probe-agent
	procd_set_param env PROBE_SERVER="$PROBE_SERVER" PROBE_TOKEN="$PROBE_TOKEN" PROBE_NAME="$PROBE_NAME" \
		PROBE_LOCATION="${PROBE_LOCATION:-}" PROBE_ISP="${PROBE_ISP:-}" PROBE_TAGS="${PROBE_TAGS:-}"
	# restart forever, 5s apart; a run longer than 1h resets the counter
	procd_set_param respawn 3600 5 0
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_close_instance
}
INIT
chmod 755 /etc/init.d/probe-agent
/etc/init.d/probe-agent enable
/etc/init.d/probe-agent start
sleep 2
if pgrep -f "^$BIN" >/dev/null 2>&1 || pidof probe-agent >/dev/null 2>&1; then
  echo "installed $($BIN version) and running. logs: logread -e probe-agent -f"
else
  echo "service did not start; check: logread -e probe-agent" >&2; exit 1
fi
