# ingress-poc

Kubernetes-native ingress gateway management platform with a control-plane/data-plane architecture. Manages fleets of Envoy and Kong gateway instances via custom Kubernetes operators, with dual management paths: a web console and GitOps.

## Quick Reference — Stop & Start

```bash
# ── Stop (keeps all data) ─────────────────────────────────────────────────────
docker stop ingress-cp-control-plane

# ── Start again ───────────────────────────────────────────────────────────────
docker start ingress-cp-control-plane
# Allow ~30 seconds for pods to come back, then open http://localhost:3000

# Check which fleet pods are running
kubectl get deployments -n ingress-dp

# ── Full reset (wipes all data and rebuilds from scratch) ─────────────────────
kind delete cluster --name ingress-cp
./k8s/kind/setup.sh
```

---

## Architecture Overview

```
Console UI  ───>  management-api  ───>  Git repo  ───>  Argo CD  ───>  Data-Plane Cluster(s)
                        │                                                       │
                    Postgres                                            Ingress Operator
                  (audit, health)                                    (Fleet/Route CRDs)
                                                                           │
                                                                  Envoy / Kong Pods
```

- **Control-plane cluster**: management-api, auth-service, console, envoy-control-plane, Argo CD, supporting services
- **Data-plane cluster(s)**: gateway pods managed by the ingress-operator watching Fleet/Route CRDs
- **Git-first**: every console change commits to the GitOps repo; Git is the source of truth for desired state
- **Multi-region ready**: one Argo CD Application per data-plane cluster, Terraform modules parameterized for N regions

### Key Terms

| Term | Meaning |
|------|---------|
| **CRD** | Custom Resource Definition -- extends Kubernetes with new resource types. This project defines `Fleet` and `Route` CRDs so you can manage gateways with `kubectl get fleets` just like built-in resources. The ingress-operator watches these CRDs and creates/scales gateway pods in response. |
| **Control plane** | The cluster running management services (API, console, Argo CD, Postgres). Does not serve end-user traffic. |
| **Data plane** | The cluster(s) running the actual gateway pods (Envoy/Kong) that handle live traffic. Managed by the control plane. |
| **GitOps** | A pattern where Git is the source of truth for cluster desired state. Changes are merged as PRs; Argo CD syncs them to the cluster automatically. |
| **kind** | Kubernetes IN Docker -- runs local K8s clusters as Docker containers. Used for development and testing. |
| **Kustomize** | A Kubernetes-native tool for customizing manifests using overlays (e.g., local vs AWS) without modifying the base YAML. |

---

## Part 1 -- Local Development and Testing (macOS)

This section takes you from a bare Mac to a fully running two-cluster Kubernetes environment.

### 1.1 Install Prerequisites

You need five tools: Homebrew, Docker, Go, kind, and kubectl. Install them in order.

**Homebrew** (macOS package manager):

```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```

Follow the post-install instructions printed by the script to add Homebrew to your PATH.

**Docker Desktop** (container runtime -- required by kind):

```bash
brew install --cask docker
```

Open Docker Desktop from Applications after install (or run `open -a "Docker Desktop"` from your terminal). **Wait** for the whale icon in the menu bar to stop animating and show "Docker Desktop is running" before continuing. This can take 30-60 seconds on first launch. Verify the daemon is responsive:

```bash
docker info | head -5
# You should see "Server Version:" in the output. If you get a socket error, Docker Desktop is not ready yet -- wait and retry.
```

**Go** (required to build the services):

```bash
brew install go
```

Verify:

```bash
go version
# Should print go1.25 or later
```

**kind** (Kubernetes in Docker -- runs local clusters):

```bash
brew install kind
```

**kubectl** (Kubernetes CLI):

```bash
brew install kubectl
```

**Git** (should already be on macOS, but verify):

```bash
git --version
```

If missing: `brew install git`.

### 1.2 Clone the Repository

```bash
git clone <repo-url> ingress-poc
cd ingress-poc
```

### 1.3 Choose Your Mode

There are two ways to run locally. Pick one.

---

#### Option A: Docker Compose (simple, single-machine)

This runs all 16 services directly in Docker containers. No Kubernetes involved.

**Start:**

```bash
docker compose up --build
```

First run builds all images and takes several minutes. Subsequent runs use cached layers.

**Stop:**

Press `Ctrl+C`, or from another terminal:

```bash
docker compose down
```

**Restart (after the environment is already set up):**

```bash
docker compose up
```

Add `--build` if you changed any Dockerfiles or Go source.

**Service URLs:**

| Service | URL |
|---------|-----|
| Console UI | http://localhost:3000 |
| Management API | http://localhost:8003 |
| Auth Service | http://localhost:8001 |
| Jaeger (tracing) | http://localhost:16686 |
| Envoy Gateway | http://localhost:8000 |
| Kong Gateway | http://localhost:8100 |
| PostgreSQL | localhost:5432 (user: `ingress`, password: `ingress_poc`, db: `ingress_registry`) |

---

#### Option B: Kubernetes with kind (production-like)

This creates a single kind cluster with namespace separation: control-plane services run in the `ingress-cp` namespace, and the ingress-operator manages fleet gateway pods in the `ingress-dp` namespace. On AWS, these map to separate EKS clusters.

**First-time setup:**

```bash
# Build Go dependencies
go mod download

# Create cluster, build images, deploy everything
# Pass GitHub credentials to enable per-fleet GitOps repos:
GITOPS_GITHUB_TOKEN=your_token GITOPS_GITHUB_USERNAME=your_username ./k8s/kind/setup.sh
```

This script:
1. Creates the `ingress-cp` kind cluster
2. Builds all 16 Docker images
3. Loads images into the cluster
4. Installs Fleet and Route CRDs
5. Creates `ingress-cp` and `ingress-dp` namespaces
6. Deploys all services (management-api, console, operator, gateways, etc.)
7. The management-api auto-deploys key fleets, creating real gateway pods in `ingress-dp`

The first run takes 10-15 minutes (image builds). Subsequent runs skip cluster creation if it already exists.

**Service URLs:**

| Service | URL |
|---------|-----|
| Console UI | http://localhost:3000 |
| Management API | http://localhost:8003 |
| Auth Service | http://localhost:8001 |
| Jaeger (tracing) | http://localhost:16686 |
| Envoy Gateway (shared) | http://localhost:8000 |
| Kong Gateway (shared) | http://localhost:8100 |
| Mock Akamai GTM (traffic entry) | http://localhost:8010 |

**Shutdown and restart:**

```bash
# ── Suspend (fast — preserves all data) ──────────────────────────────────────
# Pause the cluster without deleting it. All pods stop; Postgres data survives.
docker stop ingress-cp-control-plane

# Resume a suspended cluster (~30 seconds for pods to come back)
docker start ingress-cp-control-plane
kubectl get pods -n ingress-cp --context kind-ingress-cp   # watch until Ready

# ── Full teardown and clean restart ──────────────────────────────────────────
# Delete the cluster entirely (all data is lost — Postgres PVC lives inside Kind).
kind delete cluster --name ingress-cp

# Recreate from scratch: builds images, re-seeds DB, auto-starts core fleets.
./k8s/kind/setup.sh
```

On a clean restart the following fleet pods are brought up automatically in `ingress-dp`:

| Fleet   | ID              | LOB      |
|---------|-----------------|----------|
| JPMM    | `fleet-jpmm`    | Markets  |
| JPMA    | `fleet-access`  | Payments |
| JPMDB   | `fleet-digital` | Payments |
| AuthN   | `fleet-authn`   | xCIB     |
| AuthZ   | `fleet-authz`   | xCIB     |
| Console | `fleet-console` | xCIB     |

All other fleets appear in the Console with their configuration intact but have no running pods until deployed via the UI. To add a fleet to the auto-start list, edit the `autoDeployFleets` map in `cmd/management-api/seed.go`.

**Test the end-to-end traffic path:**

```bash
# Request flows: GTM → Edge → PSaaS → fleet-specific Envoy → backend
curl -H "Host: access.jpm.com" http://localhost:8010/
# Should return JSON from svc-web

curl -H "Host: jpmm.jpm.com" http://localhost:8010/events
# Should return JSON from svc-web (via JPMM fleet gateway)
```

**Check cluster status:**

```bash
# Control-plane pods
kubectl get pods -n ingress-cp --context kind-ingress-cp

# Data-plane gateway pods (created by the ingress-operator)
kubectl get pods -n ingress-dp --context kind-ingress-cp

# Fleet and Route custom resources
kubectl get fleets -n ingress-dp --context kind-ingress-cp
```

**Restart (cluster already exists):**

If the kind cluster is already running (you rebooted or Docker Desktop restarted):

```bash
# Check if cluster exists
kind get clusters

# If it shows ingress-cp, just re-deploy:
kubectl apply -k k8s/overlays/local/ --context kind-ingress-cp
kubectl apply -f k8s/base/ingress-operator/ --context kind-ingress-dp -n ingress-cp
```

If you changed Go source code and need to rebuild:

```bash
# Rebuild a single image (e.g., management-api)
docker build -t ingress-poc/management-api:latest -f management-api/Dockerfile .

# Load into the cluster
kind load docker-image ingress-poc/management-api:latest --name ingress-cp

# Restart the deployment to pick up the new image
kubectl rollout restart deployment/management-api -n ingress-cp --context kind-ingress-cp
```

To rebuild and reload everything:

```bash
./k8s/kind/setup.sh
```

The script skips cluster creation if it already exists and rebuilds all images.

**Stop:**

```bash
# Stop the cluster (preserves state -- fast restart later)
docker stop ingress-cp-control-plane

# Restart stopped cluster
docker start ingress-cp-control-plane
```

**Tear down completely:**

```bash
./k8s/kind/teardown.sh
```

> **Warning:** This deletes both kind clusters and everything inside them, **including the Postgres database and all its data**. In the local kind setup, Postgres runs as a pod inside the cluster -- there is no external database. All fleets, routes, audit logs, and configuration stored in the database will be lost. This is expected for local dev; the environment is designed to be ephemeral and re-created from scratch with `setup.sh`.

### 1.4 Switching Between Docker Compose and kind

The two modes are independent. Docker Compose uses ports directly; kind uses NodePort mappings to the same host ports. Do not run both at the same time -- they will conflict on ports.

```bash
# Stop Docker Compose before starting kind
docker compose down

# Or stop kind before starting Docker Compose
./k8s/kind/teardown.sh
```

### 1.5 Switching Orchestration Mode

The management-api supports two orchestration backends controlled by the `ORCHESTRATION_MODE` environment variable:

- `docker` (default): manages fleet gateways as Docker containers via the Docker Engine API
- `kubernetes`: manages fleet gateways by committing Fleet/Route CRD manifests to a GitOps repo

In Docker Compose mode, it defaults to `docker`. In the kind Kustomize overlay, it defaults to `docker` as well. To switch to the Kubernetes GitOps flow in kind:

```bash
# Set the env var on the management-api deployment
kubectl set env deployment/management-api \
  ORCHESTRATION_MODE=kubernetes \
  GITOPS_REPO_PATH=/tmp/gitops-repo \
  --context kind-ingress-cp -n ingress-cp
```

---

## Part 2 -- Production / UAT Deployment (AWS)

This section covers deploying to AWS EKS using Terraform. The architecture creates:

- 1 VPC with public and private subnets across 3 AZs
- 1 EKS control-plane cluster (management services)
- 1+ EKS data-plane cluster(s) (gateway workloads)
- 1 RDS PostgreSQL instance (multi-AZ optional)
- IAM roles with IRSA for secure cross-cluster communication

### 2.1 Install Prerequisites

In addition to the local dev prerequisites (Docker, Go, kubectl), you need:

**AWS CLI:**

```bash
brew install awscli
```

Configure credentials:

```bash
aws configure
# Enter your AWS Access Key ID, Secret Access Key, region (e.g., us-east-1), and output format (json)
```

Verify:

```bash
aws sts get-caller-identity
```

**Terraform:**

```bash
brew install terraform
```

Verify:

```bash
terraform version
# Should print 1.5 or later
```

**Argo CD CLI (optional -- for managing Argo CD from the command line):**

```bash
brew install argocd
```

### 2.2 Configure Terraform Variables

Edit the dev environment variables:

```bash
cd infra/terraform/environments/dev
```

Review and update `terraform.tfvars`:

```hcl
aws_region  = "us-east-1"         # Your preferred region
environment = "dev"                # dev, uat, or prod
project     = "ingress-poc"

# VPC
vpc_cidr           = "10.0.0.0/16"
single_nat_gateway = true          # Set false for prod (one NAT per AZ)

# EKS Control Plane
cp_cluster_name        = "ingress-cp-dev"
cp_cluster_version     = "1.29"
cp_node_instance_types = ["t3.medium"]
cp_node_min_size       = 2
cp_node_max_size       = 4
cp_node_desired_size   = 2

# EKS Data Plane Clusters
dp_clusters = {
  primary = {
    cluster_name   = "ingress-dp-dev"
    instance_types = ["c5.large"]
    min_size       = 2
    max_size       = 20
    desired_size   = 2
    node_labels = {
      "ingress.io/role"   = "dataplane"
      "ingress.io/region" = "primary"
    }
  }
  # Uncomment for multi-region:
  # secondary = {
  #   cluster_name   = "ingress-dp-dev-eu"
  #   instance_types = ["c5.large"]
  #   min_size       = 2
  #   max_size       = 10
  #   desired_size   = 2
  #   node_labels = {
  #     "ingress.io/role"   = "dataplane"
  #     "ingress.io/region" = "secondary"
  #   }
  # }
}

# RDS
rds_instance_class    = "db.t3.medium"
rds_engine_version    = "16"
rds_multi_az          = false      # Set true for prod
rds_allocated_storage = 20
rds_db_name           = "ingress"
```

For a UAT or production environment, copy the `dev/` directory:

```bash
cp -r infra/terraform/environments/dev infra/terraform/environments/uat
# Edit uat/terraform.tfvars with appropriate values
```

### 2.3 Provision AWS Infrastructure

```bash
cd infra/terraform/environments/dev

# Initialize Terraform (downloads providers)
terraform init

# Preview what will be created
terraform plan

# Create the infrastructure (takes 15-20 minutes)
terraform apply
```

Type `yes` when prompted. Terraform creates the VPC, EKS clusters, RDS instance, and IAM roles.

**After apply, configure kubectl:**

```bash
# Control-plane cluster
aws eks update-kubeconfig --name ingress-cp-dev --region us-east-1 --alias ingress-cp

# Data-plane cluster
aws eks update-kubeconfig --name ingress-dp-dev --region us-east-1 --alias ingress-dp
```

Verify:

```bash
kubectl get nodes --context ingress-cp
kubectl get nodes --context ingress-dp
```

### 2.4 Build and Push Container Images

Push images to Amazon ECR (or your preferred registry). Create ECR repositories first:

```bash
AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
AWS_REGION=us-east-1

# Login to ECR
aws ecr get-login-password --region ${AWS_REGION} | \
  docker login --username AWS --password-stdin ${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com

# Create repositories and push images
for svc in management-api auth-service envoy-control-plane kong-admin-proxy \
           console gateway-envoy gateway-kong opa svc-web svc-api watchdog \
           mock-akamai-gtm mock-akamai-edge mock-psaas ingress-operator; do

  aws ecr create-repository --repository-name ingress-poc/${svc} --region ${AWS_REGION} 2>/dev/null || true

  docker tag ingress-poc/${svc}:latest \
    ${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com/ingress-poc/${svc}:latest

  docker push ${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com/ingress-poc/${svc}:latest
done
```

### 2.5 Create the AWS Kustomize Overlay

Create an overlay for your AWS environment. A starter is at `k8s/overlays/dev/`. Update image references to point to your ECR registry:

```bash
cd k8s/overlays/dev

# Edit kustomization.yaml to set your ECR image prefix and RDS connection string
```

Key configuration changes from local:
- `ORCHESTRATION_MODE=kubernetes`
- `DATABASE_URL` pointing to RDS endpoint (from Terraform output)
- `GITOPS_REPO_PATH` or `GITOPS_REPO_URL` for the GitOps repository
- Image references to ECR
- Remove the postgres Deployment (using RDS instead)

### 2.6 Deploy to AWS

**Deploy CRDs and control-plane services:**

```bash
# Apply CRDs to both clusters
kubectl apply -f k8s/crds/ --context ingress-cp
kubectl apply -f k8s/crds/ --context ingress-dp

# Deploy control-plane services
kubectl apply -k k8s/overlays/dev/ --context ingress-cp

# Deploy the ingress-operator to data-plane
kubectl apply -f k8s/base/ingress-operator/ --context ingress-dp -n ingress-cp
```

**Install Argo CD on the control-plane cluster:**

```bash
kubectl create namespace argocd --context ingress-cp --dry-run=client -o yaml | \
  kubectl apply --context ingress-cp -f -

kubectl apply -n argocd \
  -f https://raw.githubusercontent.com/argoproj/argo-cd/v2.13.3/manifests/install.yaml \
  --context ingress-cp

kubectl rollout status deployment/argocd-server -n argocd --timeout=300s --context ingress-cp
```

**Register the data-plane cluster with Argo CD:**

```bash
# Get the DP cluster endpoint
DP_SERVER=$(aws eks describe-cluster --name ingress-dp-dev --query 'cluster.endpoint' --output text)

# Register with argocd CLI
argocd cluster add ingress-dp --name ingress-dp --server ${DP_SERVER}
```

**Configure the Argo CD Application:**

Edit `k8s/argocd/application.yaml` and replace the placeholder values:
- `PLACEHOLDER_REPO_URL` with your GitOps repository URL
- `PLACEHOLDER_DP_CLUSTER_SERVER_URL` with the DP cluster endpoint

```bash
kubectl apply -f k8s/argocd/application.yaml --context ingress-cp
```

### 2.7 Expose Services

For AWS, use an Application Load Balancer. Install the AWS Load Balancer Controller:

```bash
# The IRSA role was created by Terraform
# Install the controller via Helm
helm repo add eks https://aws.github.io/eks-charts
helm install aws-load-balancer-controller eks/aws-load-balancer-controller \
  -n kube-system --context ingress-cp \
  --set clusterName=ingress-cp-dev \
  --set serviceAccount.create=false \
  --set serviceAccount.name=aws-load-balancer-controller
```

Then create Ingress resources or use `kubectl port-forward` for initial testing:

```bash
# Quick access via port-forward
kubectl port-forward svc/console 3000:80 -n ingress-cp --context ingress-cp &
kubectl port-forward svc/management-api 8003:8003 -n ingress-cp --context ingress-cp &
```

### 2.8 Restart (Environment Already Provisioned)

If the AWS infrastructure is already up and you need to restart services after a code change:

**Rebuild and push a single image:**

```bash
AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
AWS_REGION=us-east-1
SVC=management-api

docker build -t ingress-poc/${SVC}:latest -f management-api/Dockerfile .
docker tag ingress-poc/${SVC}:latest \
  ${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com/ingress-poc/${SVC}:latest
docker push ${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com/ingress-poc/${SVC}:latest

kubectl rollout restart deployment/${SVC} -n ingress-cp --context ingress-cp
```

**Redeploy all services (no infrastructure changes):**

```bash
kubectl apply -k k8s/overlays/dev/ --context ingress-cp
kubectl apply -f k8s/base/ingress-operator/ --context ingress-dp -n ingress-cp
```

**Apply infrastructure changes (Terraform):**

```bash
cd infra/terraform/environments/dev
terraform plan    # Review changes
terraform apply   # Apply changes
```

**Check status:**

```bash
# Pods
kubectl get pods -n ingress-cp --context ingress-cp
kubectl get pods -n ingress-cp --context ingress-dp

# Argo CD sync status
kubectl get applications -n argocd --context ingress-cp

# Fleet and Route CRDs
kubectl get fleets,routes -n ingress-cp --context ingress-dp
```

### 2.9 Tear Down AWS Environment

```bash
# Remove K8s workloads first (avoids orphaned AWS resources like load balancers)
kubectl delete -k k8s/overlays/dev/ --context ingress-cp
kubectl delete -f k8s/base/ingress-operator/ --context ingress-dp -n ingress-cp

# Destroy infrastructure
cd infra/terraform/environments/dev
terraform destroy
```

Type `yes` when prompted. This deletes **all** AWS resources including EKS clusters, RDS, VPC, and IAM roles. Unlike the local setup, `terraform destroy` **also deletes the RDS Postgres database and all its data**. The EKS clusters alone can be torn down and recreated without affecting RDS -- only `terraform destroy` removes the database. For production, consider enabling RDS deletion protection and automated backups in `terraform.tfvars` before the first deploy.

---

## Service Reference

| Service | Port | Description |
|---------|------|-------------|
| Console UI | 3000 | React web interface for fleet/route management |
| Management API | 8003 | Control-plane orchestrator |
| Auth Service | 8001 | OIDC/PKCE identity provider |
| Envoy Control Plane | 8080 | REST xDS server for Envoy gateways |
| Kong Admin Proxy | 8102 | Proxy to Kong admin API |
| Gateway Envoy | 8000 | L4 Envoy gateway (admin: 9901) |
| Gateway Kong | 8100 | L4 Kong gateway (admin: 8101) |
| OPA | 8181 | Policy evaluation engine |
| svc-web | 8004 | Mock backend service |
| svc-api | 8005 | Mock backend service |
| Watchdog | 8006 | Health monitoring |
| Jaeger | 16686 | Distributed tracing UI |
| PostgreSQL | 5432 | Database (local only; RDS on AWS) |
| Mock Akamai GTM | 8010 | CDN simulation layer 1 |
| Mock Akamai Edge | 8011 | CDN simulation layer 2 |
| Mock PSaaS | 8012 | Regional perimeter simulation |
| Argo CD | 30443 | GitOps continuous delivery (kind) |

## Database Connection

| Field | Value |
|-------|-------|
| Host | `localhost` (local) or RDS endpoint (AWS) |
| Port | 5432 |
| Database | `ingress_registry` |
| User | `ingress` |
| Password | `ingress_poc` (local) or Secrets Manager (AWS) |
| Connection URL | `postgresql://ingress:ingress_poc@localhost:5432/ingress_registry` |

## Project Structure

```
ingress-poc/
  cmd/
    management-api/        # Control-plane API server
    ingress-operator/      # K8s operator for Fleet/Route CRDs
    auth-service/          # OIDC identity provider
    envoy-control-plane/   # xDS server for Envoy
    kong-admin-proxy/      # Kong admin API proxy
    svc-web/, svc-api/     # Mock backends
    watchdog/              # Health monitoring
    mock-akamai-*/         # CDN simulation
  console/                 # React UI (Vite + TailwindCSS)
  k8s/
    crds/                  # Fleet and Route CRD definitions
    base/                  # Kustomize base manifests (all services)
    overlays/
      local/               # kind NodePort patches
      dev/                 # AWS dev patches
    kind/                  # kind cluster configs and setup scripts
    argocd/                # Argo CD install and application configs
  infra/terraform/
    modules/               # Reusable modules (vpc, eks, rds, irsa)
    environments/dev/      # Dev environment Terraform config
  migrations/              # PostgreSQL schema migrations
  docker-compose.yml       # Docker Compose for simple local dev
```

## Troubleshooting

**Pods stuck in ImagePullBackOff (kind):**
Images need to be loaded into kind clusters. Run `kind load docker-image ingress-poc/<service>:latest --name <cluster>` or re-run `./k8s/kind/setup.sh`.

**Port conflict between Docker Compose and kind:**
Stop one before starting the other. They use the same host ports.

**`docker.sock` not found (Docker Compose mode):**
Start Docker Desktop and wait until the whale icon shows "running". Verify with `docker info`.

**kind clusters not starting after Docker Desktop restart:**
Docker Desktop restarts kind containers automatically. If not, run: `docker start ingress-cp-control-plane ingress-dp-control-plane ingress-dp-worker ingress-dp-worker2`

**Terraform state issues:**
For team environments, uncomment the S3 backend in `infra/terraform/environments/dev/backend.tf` and configure a shared S3 bucket + DynamoDB table for state locking.

**Argo CD Application stuck OutOfSync:**
Check the GitOps repo has manifests in the expected path (`clusters/data-plane-1/`). Verify the repo URL and cluster server URL in the Application spec.
