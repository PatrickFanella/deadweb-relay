#!/usr/bin/env sh
set -eu
BASE="${BASE_URL:-http://localhost:8080}"

printf '== index ==\n'
curl -sS "$BASE/" | head -n 8
printf '\n== options ==\n'
curl -sSI -X OPTIONS "$BASE/notes/demo"
printf '\n== leave guestbook entry ==\n'
curl -sS -X POST "$BASE/guestbook/" -d 'name=demo&body=hello+from+curl'
printf '\n\n== write note ==\n'
curl -sS -X PUT -H 'Content-Type: text/plain' --data 'persistent scratch note' "$BASE/notes/demo/scratch"
printf '\n\n== read text wall ==\n'
curl -sS -H 'Accept: text/plain' "$BASE/wall/"
