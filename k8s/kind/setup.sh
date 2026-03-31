#!/usr/bin/env bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

echo "=== Ingress PoC Kind Cluster Setup ==="
echo "Project root: ${PROJECT_ROOT}"

# ------------------------------------------------------------------
# 1. Pre-flight checks
# ------------------------------------------------------------------
if ! command -v kind &>/dev/null; then
  echo "ERROR: kind is not installed. Install it from https://kind.sigs.k8s.io/"
  exit 1
fi

if ! command -v kubectl &>/dev/null; then
  echo "ERROR: kubectl is not installed."
  exit 1
fi

if ! command -v docker &>/dev/null; then
  echo "ERROR: docker is not installed."
  exit 1
fi

# ------------------------------------------------------------------
# 2. Create cluster (single cluster for local dev)
# ------------------------------------------------------------------
echo ""
echo "--- Creating Kind cluster ---"

if kind get clusters 2>/dev/null | grep -q "^ingress-cp$"; then
  echo "Cluster ingress-cp already exists, skipping creation."
else
  echo "Creating cluster (ingress-cp)..."
  kind create cluster --config "${SCRIPT_DIR}/cp-config.yaml"
fi

# ------------------------------------------------------------------
# 3. Install CRDs
# ------------------------------------------------------------------
echo ""
echo "--- Installing CRDs ---"
kubectl apply -f "${PROJECT_ROOT}/k8s/crds/" --context kind-ingress-cp

# ------------------------------------------------------------------
# 4. Build Docker images
# ------------------------------------------------------------------
echo ""
echo "--- Building Docker images ---"

# Each entry: "service-name context dockerfile"
# All paths are relative to PROJECT_ROOT.
# When context is a subdirectory (e.g. ./console), the dockerfile path
# must still be relative to PROJECT_ROOT so the -f flag resolves correctly.
IMAGE_LIST="
management-api       .              management-api/Dockerfile
auth-service         .              auth-service/Dockerfile
envoy-control-plane  .              envoy-control-plane/Dockerfile
kong-admin-proxy     .              kong-admin-proxy/Dockerfile
console              ./console      console/Dockerfile
gateway-envoy        ./gateway-envoy gateway-envoy/Dockerfile
gateway-kong         ./gateway-kong  gateway-kong/Dockerfile
opa                  ./opa           opa/Dockerfile
svc-web              .              svc-web/Dockerfile
svc-api              .              svc-api/Dockerfile
watchdog             .              watchdog/Dockerfile
mock-akamai-gtm      .              mock-akamai-gtm/Dockerfile
mock-akamai-edge     .              mock-akamai-edge/Dockerfile
mock-psaas           .              mock-psaas/Dockerfile
dns                  ./dns           dns/Dockerfile
ingress-operator     .              cmd/ingress-operator/Dockerfile
"

echo "${IMAGE_LIST}" | while read -r svc ctx dockerfile; do
  [ -z "${svc}" ] && continue
  tag="ingress-poc/${svc}:latest"
  echo "Building ${tag} (context=${ctx}, dockerfile=${dockerfile})..."
  docker build -t "${tag}" -f "${PROJECT_ROOT}/${dockerfile}" "${PROJECT_ROOT}/${ctx}"
done

# ------------------------------------------------------------------
# 5. Load images into cluster
# ------------------------------------------------------------------
echo ""
echo "--- Loading images into Kind cluster ---"

echo "${IMAGE_LIST}" | while read -r svc ctx dockerfile; do
  [ -z "${svc}" ] && continue
  img="ingress-poc/${svc}:latest"
  echo "Loading ${img} into ingress-cp..."
  kind load docker-image "${img}" --name ingress-cp
done

# ------------------------------------------------------------------
# 6. Create namespaces
# ------------------------------------------------------------------
echo ""
echo "--- Creating namespaces ---"

kubectl create namespace ingress-cp --context kind-ingress-cp --dry-run=client -o yaml | \
  kubectl apply --context kind-ingress-cp -f -
kubectl create namespace ingress-dp --context kind-ingress-cp --dry-run=client -o yaml | \
  kubectl apply --context kind-ingress-cp -f -

# ------------------------------------------------------------------
# 7. Create GitHub secret (if not already present)
# ------------------------------------------------------------------
echo ""
echo "--- Checking GitHub secret ---"
if kubectl get secret gitops-github -n ingress-cp --context kind-ingress-cp &>/dev/null; then
  echo "GitHub secret already exists."
else
  if [ -n "${GITOPS_GITHUB_TOKEN}" ] && [ -n "${GITOPS_GITHUB_USERNAME}" ]; then
    echo "Creating GitHub secret from environment variables..."
    kubectl create secret generic gitops-github \
      --from-literal=token="${GITOPS_GITHUB_TOKEN}" \
      --from-literal=username="${GITOPS_GITHUB_USERNAME}" \
      -n ingress-cp --context kind-ingress-cp
  else
    echo "No GITOPS_GITHUB_TOKEN/GITOPS_GITHUB_USERNAME env vars set. Skipping GitHub secret."
    echo "To enable GitHub integration, run:"
    echo "  kubectl create secret generic gitops-github \\"
    echo "    --from-literal=token=YOUR_TOKEN \\"
    echo "    --from-literal=username=YOUR_USERNAME \\"
    echo "    -n ingress-cp --context kind-ingress-cp"
  fi
fi

# ------------------------------------------------------------------
# 7b. Create TLS secret for *.jpm.com certs
# ------------------------------------------------------------------
echo ""
echo "--- Checking TLS certs secret ---"
if kubectl get secret jpm-tls-certs -n ingress-cp --context kind-ingress-cp &>/dev/null; then
  echo "TLS certs secret already exists."
else
  if [ -f "${PROJECT_ROOT}/certs/jpm.com.crt" ] && [ -f "${PROJECT_ROOT}/certs/jpm.com.key" ]; then
    echo "Creating TLS certs secret from certs/ directory..."
    kubectl create secret generic jpm-tls-certs \
      --from-file=jpm.com.crt="${PROJECT_ROOT}/certs/jpm.com.crt" \
      --from-file=jpm.com.key="${PROJECT_ROOT}/certs/jpm.com.key" \
      -n ingress-cp --context kind-ingress-cp
  else
    echo "No certs/jpm.com.crt or certs/jpm.com.key found. HTTPS for *.jpm.com will not work."
  fi
fi

# ------------------------------------------------------------------
# 8. Deploy all services (CP + operator in single cluster)
# ------------------------------------------------------------------
echo ""
echo "--- Deploying all services ---"
kubectl apply -k "${PROJECT_ROOT}/k8s/overlays/local/" --context kind-ingress-cp

# ------------------------------------------------------------------
# 9. Print status
# ------------------------------------------------------------------
echo ""
echo "=== Setup Complete ==="
echo ""
echo "Control-plane services (ingress-cp namespace):"
kubectl get pods -n ingress-cp --context kind-ingress-cp 2>/dev/null || true
echo ""
echo "Data-plane resources (ingress-dp namespace):"
kubectl get pods -n ingress-dp --context kind-ingress-cp 2>/dev/null || true
echo ""
echo "Access points:"
echo "  Console:        http://localhost:3000"
echo "  Management API: http://localhost:8003"
echo "  Auth Service:   http://localhost:8001"
echo "  Jaeger UI:      http://localhost:16686"
echo "  Gateway Envoy:  http://localhost:8000"
echo "  Gateway Kong:   http://localhost:8100"
echo "  Mock GTM:       http://localhost:8010"
echo ""
echo "To test the traffic path:"
echo "  curl -H 'Host: jpmm.jpm.com' http://localhost:8010/research"
