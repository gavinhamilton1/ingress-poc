#!/usr/bin/env bash
set -euo pipefail

# Configures this Mac to resolve *.jpm.com to the platform's local CoreDNS
# container and trusts its self-signed TLS cert, so the console and any
# onboarded routes can be reached by their real hostname
# (https://jpm.com:3443, https://demo-user-api.jpm.com, etc.) instead of
# only via `curl -H "Host: ..."` against localhost.
#
# Neither step is done by `docker compose up` itself — they're one-time,
# sudo-requiring changes to this specific machine, not the repo. Safe to
# re-run; both checks are idempotent.
#
# Usage: ./scripts/setup-local-network.sh

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CERT_PATH="$REPO_ROOT/certs/jpm.com.crt"
RESOLVER_FILE="/etc/resolver/jpm.com"
DNS_PORT=5553

if [[ "$(uname)" != "Darwin" ]]; then
  echo "This script only supports macOS (it uses /etc/resolver and the macOS Keychain)." >&2
  exit 1
fi

echo "== 1. DNS resolver: *.jpm.com -> 127.0.0.1:${DNS_PORT} =="
if [[ -f "$RESOLVER_FILE" ]] && grep -q "127.0.0.1" "$RESOLVER_FILE" && grep -q "$DNS_PORT" "$RESOLVER_FILE"; then
  echo "Already configured: $RESOLVER_FILE"
else
  echo "Creating $RESOLVER_FILE (requires sudo)..."
  sudo mkdir -p /etc/resolver
  printf 'nameserver 127.0.0.1\nport %s\n' "$DNS_PORT" | sudo tee "$RESOLVER_FILE" > /dev/null
  echo "Created."
fi

echo
echo "== 2. Trusting the jpm.com TLS cert in the System keychain =="
if [[ ! -f "$CERT_PATH" ]]; then
  echo "Cert not found at $CERT_PATH — run this script from inside the repo." >&2
  exit 1
fi
if security verify-cert -c "$CERT_PATH" >/dev/null 2>&1; then
  echo "Already trusted."
else
  echo "Adding to the System keychain (requires sudo + a password/Touch ID prompt)..."
  sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain "$CERT_PATH"
  echo "Trusted."
fi

echo
echo "Flushing the DNS cache so the new resolver takes effect immediately..."
sudo killall -HUP mDNSResponder 2>/dev/null || true

cat <<'EOF'

Done. Once the platform is running (docker compose up --build, or at
least its "dns" container), verify with:

  dscacheutil -q host -a name jpm.com
  # should print ip_address: 127.0.0.1

  curl -sk https://jpm.com:3443/login -o /dev/null -w '%{http_code}\n'
  # should print 200, with no certificate warning in a real browser
EOF
