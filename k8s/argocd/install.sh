#!/usr/bin/env bash
set -e

ARGOCD_VERSION="v2.13.3"

echo "=== Argo CD Installation (Control-Plane Cluster) ==="

# ------------------------------------------------------------------
# 1. Switch to CP context
# ------------------------------------------------------------------
echo "Switching to kind-ingress-cp context..."
kubectl config use-context kind-ingress-cp

# ------------------------------------------------------------------
# 2. Create argocd namespace
# ------------------------------------------------------------------
echo "Creating argocd namespace..."
kubectl create namespace argocd --dry-run=client -o yaml | kubectl apply -f -

# ------------------------------------------------------------------
# 3. Install Argo CD
# ------------------------------------------------------------------
echo "Installing Argo CD ${ARGOCD_VERSION}..."
kubectl apply -n argocd \
  -f "https://raw.githubusercontent.com/argoproj/argo-cd/${ARGOCD_VERSION}/manifests/install.yaml"

# ------------------------------------------------------------------
# 4. Wait for argocd-server to be ready
# ------------------------------------------------------------------
echo "Waiting for argocd-server deployment to be ready..."
kubectl rollout status deployment/argocd-server -n argocd --timeout=300s

# ------------------------------------------------------------------
# 5. Patch argocd-server to NodePort
# ------------------------------------------------------------------
echo "Patching argocd-server service to NodePort on port 30443..."
kubectl patch svc argocd-server -n argocd --type='json' -p='[
  {"op": "replace", "path": "/spec/type", "value": "NodePort"},
  {"op": "replace", "path": "/spec/ports/0/nodePort", "value": 30443}
]'

# ------------------------------------------------------------------
# 6. Print access information
# ------------------------------------------------------------------
echo ""
echo "=== Argo CD Installed ==="
echo ""
echo "Retrieve the initial admin password with:"
echo "  kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d && echo"
echo ""
echo "Access Argo CD UI at:"
echo "  https://localhost:30443"
echo ""
echo "Login with:"
echo "  Username: admin"
echo "  Password: (use the command above)"
