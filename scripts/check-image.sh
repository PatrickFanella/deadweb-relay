#!/usr/bin/env bash
set -euo pipefail
image=${1:?image is required}
fixture=$(docker create --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true --tmpfs /tmp:mode=1777 \
  --mount type=tmpfs,destination=/app/data,tmpfs-mode=1777 \
  -e SITE_NAME='CI restore fixture' -e ADMIN_TOKEN=ci-fixture-not-production \
  -e OBSERVATORY_HMAC_KEY=ci-fixture-not-production "$image")
trap 'docker rm -f "$fixture" >/dev/null' EXIT
docker start "$fixture" >/dev/null
for _attempt in {1..20}; do
  if docker exec "$fixture" wget -q -T 2 -O /dev/null http://127.0.0.1:8080/healthz; then
    break
  fi
  sleep 0.2
done
test "$(docker exec "$fixture" id -u)" = 1000
docker exec "$fixture" wget -q -T 2 -O - http://127.0.0.1:8080/healthz | /usr/bin/grep -q '^ok$'
docker exec "$fixture" wget -q -T 2 -O - http://127.0.0.1:8080/ | /usr/bin/grep -q 'a SUBCULT project'
docker exec "$fixture" wget -q -T 2 -O /dev/null http://127.0.0.1:8080/static/brand/deadweb-relay/og.png
