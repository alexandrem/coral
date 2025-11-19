# Docker Compose Demo (demo-app + coral-agent)

This example spins up a tiny HTTP service plus a Coral agent container so you
can exercise the agent against something tangible. It assumes you already
have the Coral discovery service listening on `http://localhost:8080` and a
colony running on the host (outside of Docker).

## Components

- `demo-app`: nginx:alpine serving a simple HTML page on port 8080.
- `coral-agent`: Builds from the local repo (Dockerfile)
  and runs `coral agent start` with pre-configured services, reusing your host's colony config.

## Prerequisites

1. Build the project binaries once so Docker can copy them (e.g. `make build`).
2. Start discovery on the host: `./bin/coral-discovery --port 8080`.
3. Initialize and start a colony on the host: `./bin/coral init` then
   `./bin/coral colony start`.
4. Confirm the colony ID you want to use (see `~/.coral/colonies/`).
5. Docker Desktop (or Docker Engine 20.10+) with access to `/dev/net/tun` and
   support for `host-gateway` (Docker 20.10 enables this by default).

## Usage

```bash
cd examples/docker-compose
cp .env.example .env
# edit .env: set CORAL_COLONY_ID and HOST_CORAL_DIR (absolute path to ~/.coral)
docker compose up --build
```

Key environment variables (defined in `.env`):

- `CORAL_COLONY_ID`: Existing colony ID (e.g. `my-app-dev-a1b2c3`).
- `CORAL_DISCOVERY_ENDPOINT`: Leave as `http://host.docker.internal:8080` unless
  your discovery service runs elsewhere.
- `HOST_CORAL_DIR`: Absolute path to your host `~/.coral` directory so the agent
  container can read the colony config/secret.

Once everything is up:

- Visit `http://localhost:8080` to hit the demo app.
- Watch the `coral-agent` logs: it should resolve the colony via
  `host.docker.internal:8080`, create a WireGuard peer, and register itself.

## Troubleshooting

- **Connection refused**: ensure `./bin/coral-discovery --port 8080` and
  `./bin/coral colony start` are running on the host.
- **Permission denied creating TUN**:
  - **Colima users**: TUN device is available in the VM by default: `colima ssh -- ls -l /dev/net/tun`
  - **Linux users**: Load the TUN module: `sudo modprobe tun`
  - Docker Desktop users: TUN devices may require privileged mode
  - The container runs as root to create TUN devices (required for WireGuard mesh)
- **Cannot read colony config**: double-check `HOST_CORAL_DIR` points to the
  directory that contains `colonies/<id>.yaml`.
- **WireGuard implementation**: Coral uses wireguard-go (userspace implementation),
  so no kernel module is required. The container only needs NET_ADMIN capability
  and access to /dev/net/tun.
