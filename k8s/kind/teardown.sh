#!/usr/bin/env bash
set -e

echo "=== Ingress PoC Kind Cluster Teardown ==="

if ! command -v kind &>/dev/null; then
  echo "ERROR: kind is not installed."
  exit 1
fi

echo "Deleting cluster (ingress-cp)..."
kind delete cluster --name ingress-cp 2>/dev/null || echo "Cluster ingress-cp does not exist."

# Clean up legacy data-plane cluster if it exists from older setup
if kind get clusters 2>/dev/null | grep -q "^ingress-dp$"; then
  echo "Deleting legacy data-plane cluster (ingress-dp)..."
  kind delete cluster --name ingress-dp 2>/dev/null || true
fi

echo ""
echo "Teardown complete. All Kind clusters removed."
