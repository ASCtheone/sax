#!/usr/bin/env bash
# SPIKE: two routers (v1, v2) each running the same api on :3200 and web on
# :4200, plus a bob.lan service -> api:3200. No sudo, no loopback aliases.
set -euo pipefail
cd "$(dirname "$0")"
clang -dynamiclib -O2 -o libsaxvnet.dylib ../../internal/vnet/shim/src/shim.c ../../internal/vnet/shim/src/exec.c
SHIM="$PWD/libsaxvnet.dylib"
STATE="$(mktemp -d)"
SUBNET=10.12.12.0/24
PIDS=()
cleanup() {
  for p in "${PIDS[@]:-}"; do pkill -P "$p" 2>/dev/null; kill "$p" 2>/dev/null; done
  rm -rf "$STATE"
}
trap cleanup EXIT

mkrouter() {
  mkdir -p "$STATE/$1/ports"
  printf 'api.lan 10.12.12.4\nweb.lan 10.12.12.5\nbob.lan 10.12.12.200\n' >"$STATE/$1/names"
  printf '10.12.12.200 80 10.12.12.4 3200\n' >"$STATE/$1/svc"
}

# vrun <router> <session-ip> <cmd...>
vrun() (
  r=$1 ip=$2; shift 2
  exec env DYLD_INSERT_LIBRARIES="$SHIM" SAX_VNET_DIR="$STATE/$r" \
      SAX_VNET_IP="$ip" SAX_VNET_SUBNET="$SUBNET" "$@"
)

for r in v1 v2; do
  mkrouter $r
  vrun $r 10.12.12.4 node server.js api $r 3200 & PIDS+=($!)
  vrun $r 10.12.12.5 node server.js web $r 4200 & PIDS+=($!)
done
sleep 1

for r in v1 v2; do
  echo "== client in router $r (10.12.12.9)"
  vrun $r 10.12.12.9 node client.js \
    http://10.12.12.4:3200/x http://web.lan:4200/ http://bob.lan/api/users http://10.12.12.7:3200/
  echo "== localhost inside $r api session (10.12.12.4)"
  vrun $r 10.12.12.4 node client.js http://localhost:3200/self
done

echo "== host (no shim): nothing really listens on 3200/4200"
node client.js http://localhost:3200/ http://localhost:4200/
echo "== Go binary in v2 (10.12.12.9)"
vrun v2 10.12.12.9 ./goclient.bin http://10.12.12.4:3200/go http://bob.lan/go-name

echo "== via /bin/zsh (SIP strips DYLD_*; re-exported from SAX_VNET_SHIM)"
vrun v1 10.12.12.9 env SAX_VNET_SHIM="$SHIM" /bin/zsh -c \
  'export DYLD_INSERT_LIBRARIES=$SAX_VNET_SHIM; node client.js http://bob.lan/zsh'

echo "== real listening sockets (node processes)"
lsof -nP -iTCP -sTCP:LISTEN 2>/dev/null | awk '$1=="node" && $9 ~ /127\.0\.0\.1/ {print "  pid "$2" "$9}' || true
