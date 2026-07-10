#!/bin/sh
set -eu

api_base_url="${PALREST_API_BASE_URL:-}"

cat >/srv/config.js <<EOF
window.__PALREST_CONFIG__ = {
  API_BASE_URL: "${api_base_url}"
};
EOF

exec caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
