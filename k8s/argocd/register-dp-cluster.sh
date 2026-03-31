#!/usr/bin/env bash
set -e

echo "=== Register Data-Plane Cluster with Argo CD ==="

# ------------------------------------------------------------------
# 1. Get the DP cluster's internal Docker network IP
#    (Kind clusters communicate via the Docker bridge network)
# ------------------------------------------------------------------
echo "Discovering data-plane cluster API server address..."

DP_CONTAINER="ingress-dp-control-plane"
DP_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "${DP_CONTAINER}")

if [ -z "${DP_IP}" ]; then
  echo "ERROR: Could not determine IP for ${DP_CONTAINER}. Is the ingress-dp cluster running?"
  exit 1
fi

DP_SERVER="https://${DP_IP}:6443"
echo "Data-plane API server: ${DP_SERVER}"

# ------------------------------------------------------------------
# 2. Extract credentials from the DP kubeconfig
# ------------------------------------------------------------------
echo "Extracting DP cluster credentials..."

# Get the CA certificate
DP_CA=$(kubectl config view --raw -o jsonpath='{.clusters[?(@.name=="kind-ingress-dp")].cluster.certificate-authority-data}')

# Get the client certificate and key
DP_CERT=$(kubectl config view --raw -o jsonpath='{.users[?(@.name=="kind-ingress-dp")].user.client-certificate-data}')
DP_KEY=$(kubectl config view --raw -o jsonpath='{.users[?(@.name=="kind-ingress-dp")].user.client-key-data}')

if [ -z "${DP_CA}" ] || [ -z "${DP_CERT}" ] || [ -z "${DP_KEY}" ]; then
  echo "ERROR: Could not extract credentials for kind-ingress-dp from kubeconfig."
  exit 1
fi

# ------------------------------------------------------------------
# 3. Create the Argo CD cluster secret
# ------------------------------------------------------------------
echo "Creating Argo CD cluster secret..."

kubectl config use-context kind-ingress-cp

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: ingress-dp-cluster
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: cluster
type: Opaque
stringData:
  name: ingress-dp
  server: "${DP_SERVER}"
  config: |
    {
      "tlsClientConfig": {
        "insecure": false,
        "caData": "${DP_CA}",
        "certData": "${DP_CERT}",
        "keyData": "${DP_KEY}"
      }
    }
EOF

echo ""
echo "=== Data-Plane Cluster Registered ==="
echo ""
echo "Cluster name: ingress-dp"
echo "Server URL:   ${DP_SERVER}"
echo ""
echo "Use this server URL in your Argo CD Application destination."
