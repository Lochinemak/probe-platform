#!/bin/sh
# Forced command for the CI deploy key: pulls the requested image tag and
# restarts the dashboard.  Usage (locally): ./deploy.sh sha-abc1234 | latest
set -eu
cd /opt/probe-platform
TAG="${1:-${SSH_ORIGINAL_COMMAND:-latest}}"
case "$TAG" in
  *[!a-zA-Z0-9._-]*|"") echo "bad tag: $TAG" >&2; exit 2 ;;
esac
echo "deploying tag $TAG"
IMAGE_TAG="$TAG" docker compose pull --quiet server
IMAGE_TAG="$TAG" docker compose up -d --remove-orphans
if grep -q '^IMAGE_TAG=' .env; then sed -i "s/^IMAGE_TAG=.*/IMAGE_TAG=$TAG/" .env; else echo "IMAGE_TAG=$TAG" >> .env; fi
for i in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:8080/api/health >/dev/null 2>&1; then
    echo "healthy: $(curl -fsS http://127.0.0.1:8080/api/health)"; docker image prune -f >/dev/null; exit 0
  fi
  sleep 1
done
echo "server did not become healthy" >&2; docker compose logs --tail=30 server; exit 1
