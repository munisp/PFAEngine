#!/usr/bin/env bash
# Register (or update) the PFAEngine Postgres source connector with the
# Debezium Connect REST API. Idempotent: uses PUT .../config, so re-running
# applies the same desired state instead of failing with 409.
#
# Usage:
#   ./register.sh                      # connect at localhost:8083
#   CONNECT_URL=http://connect:8083 ./register.sh
#   ./register.sh --delete             # remove the connector
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONNECT_URL="${CONNECT_URL:-http://localhost:8083}"
CONNECTOR="pfaengine-postgres-source"
CONFIG_FILE="$HERE/postgres-source.json"

if [[ "${1:-}" == "--delete" ]]; then
  curl -fsS -X DELETE "$CONNECT_URL/connectors/$CONNECTOR" || true
  echo "connector $CONNECTOR deleted (or did not exist)"
  exit 0
fi

# Wait for Connect's REST API (plugin discovery can take a while on boot).
for _ in $(seq 1 30); do
  if curl -fsS "$CONNECT_URL/connectors" >/dev/null 2>&1; then break; fi
  echo "waiting for Connect at $CONNECT_URL ..."
  sleep 5
done

# PUT /connectors/<name>/config wants the bare config object (no "name").
python3 - "$CONFIG_FILE" "$CONNECT_URL" "$CONNECTOR" <<'PY'
import json
import sys
import urllib.request

config_file, connect_url, connector = sys.argv[1], sys.argv[2], sys.argv[3]
with open(config_file) as fh:
    payload = json.load(fh)
body = json.dumps(payload["config"]).encode()
req = urllib.request.Request(
    f"{connect_url}/connectors/{connector}/config",
    data=body,
    headers={"Content-Type": "application/json"},
    method="PUT",
)
with urllib.request.urlopen(req, timeout=30) as resp:
    out = json.load(resp)
print(f"PUT {resp.status}: connector {out['name']} registered/updated")
PY

curl -fsS "$CONNECT_URL/connectors/$CONNECTOR/status" | python3 -m json.tool
