# ingress-poc

## Running with Docker

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) with Compose (Docker Desktop includes Compose V2 as `docker compose`)

### Start the stack

From the repository root:

```bash
docker compose up --build
```

On older setups that only have Compose V1, use `docker-compose` instead of `docker compose`.

The first run builds all service images and can take several minutes. After that, `docker compose up` is enough unless Dockerfiles or dependencies change.

### Stop

Press `Ctrl+C` in the terminal running Compose, or run:

```bash
docker compose down
```

### Service URLs and ports

| Service | URL / port |
|--------|------------|
| Console UI | http://localhost:3000 |
| Jaeger UI | http://localhost:16686 |
| PostgreSQL | `localhost:5432` — user `ingress`, password `ingress_poc`, database `ingress_registry` |
| Envoy gateway | `8000` (admin UI `9901`) |
| Kong gateway | `8100` (admin `8101`) |
| Management API | `8003` |
| Auth service | `8001` |

### Notes

- **Management API** mounts `/var/run/docker.sock` so it can manage containers on the same Docker host. This matches a typical local Docker Desktop workflow.
- **mock-akamai-gtm** mounts `./certs` (`jpm.com.crt`, `jpm.com.key`) for HTTPS. The repo includes these files under `certs/`.

### Running a subset of services

To start only specific services:

```bash
docker compose up --build postgres jaeger management-api
```

Use names from the `services:` section in `docker-compose.yml`.

### Troubleshooting

**`connect: no such file or directory` for `docker.sock`**

Compose cannot reach the Docker daemon. Start **Docker Desktop** (macOS/Windows) or ensure the Docker engine service is running on Linux. Wait until Docker reports “running,” then run `docker info` — it should succeed without errors before you retry `docker compose up`.


Field	Value
Host	localhost (from your machine; use hostname postgres only from other containers on the same Compose network)
Port	5432
Database	ingress_registry
User	ingress
Password	ingress_poc
Connection URL:

postgresql://ingress:ingress_poc@localhost:5432/ingress_registry
