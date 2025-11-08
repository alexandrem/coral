# Coral Deployment Guide

This guide covers deploying Coral colonies and agents in production
environments.

## WireGuard Endpoint Configuration

### Critical Concept

Coral uses WireGuard to create a secure mesh network between colonies and
agents. Understanding the difference between **WireGuard endpoints** and **mesh
IPs** is crucial:

- **WireGuard Endpoint**: The **publicly reachable** address where the WireGuard
  UDP listener is accessible (e.g., `203.0.113.5:41580` or
  `colony.example.com:41580`)
- **Mesh IP**: The **virtual IP address** assigned inside the WireGuard tunnel (
  e.g., `10.42.0.1`, `fd42::1`)

**The mesh IPs only exist AFTER the WireGuard tunnel is established.** You
cannot use them to establish the tunnel itself.

### Local Development

For local development where colony and agents run on the same machine:

```bash
# Colony
./bin/coral colony start

# Agent (separate terminal)
./bin/coral agent start
```

Default behavior:

- Colony advertises endpoint: `127.0.0.1:41580`
- Agents connect via localhost
- ✅ Works out of the box

### Production Deployment

For production where colony and agents are on different machines, you **MUST**
set `CORAL_PUBLIC_ENDPOINT`:

```bash
CORAL_PUBLIC_ENDPOINT=<public-ip-or-hostname>:41580 coral colony start
```

#### Examples by Platform

**Bare Metal / VPS:**

```bash
# Using public IP
CORAL_PUBLIC_ENDPOINT=203.0.113.5:41580 coral colony start

# Using hostname (recommended)
CORAL_PUBLIC_ENDPOINT=colony.example.com:41580 coral colony start
```

**AWS EC2:**

```bash
# Get public IP from metadata
PUBLIC_IP=$(curl -s http://169.254.169.254/latest/meta-data/public-ipv4)
CORAL_PUBLIC_ENDPOINT=${PUBLIC_IP}:41580 coral colony start

# Or use Elastic IP / DNS
CORAL_PUBLIC_ENDPOINT=colony.example.com:41580 coral colony start
```

**GCP Compute Engine:**

```bash
# Get external IP from metadata
EXTERNAL_IP=$(curl -s -H "Metadata-Flavor: Google" \
  http://metadata.google.internal/computeMetadata/v1/instance/network-interfaces/0/access-configs/0/external-ip)
CORAL_PUBLIC_ENDPOINT=${EXTERNAL_IP}:41580 coral colony start
```

**Azure VM:**

```bash
# Get public IP from metadata
PUBLIC_IP=$(curl -s -H Metadata:true \
  "http://169.254.169.254/metadata/instance/network/interface/0/ipv4/ipAddress/0/publicIpAddress?api-version=2021-02-01&format=text")
CORAL_PUBLIC_ENDPOINT=${PUBLIC_IP}:41580 coral colony start
```

**Docker:**

```bash
# Use host machine's public IP
CORAL_PUBLIC_ENDPOINT=203.0.113.5:41580 docker run \
  -e CORAL_PUBLIC_ENDPOINT \
  -p 41580:41580/udp \
  -p 9000:9000 \
  coral/colony
```

**Kubernetes:**

```yaml
apiVersion: v1
kind: Service
metadata:
    name: coral-colony
spec:
    type: LoadBalancer
    selector:
        app: coral-colony
    ports:
        -   name: wireguard
            protocol: UDP
            port: 41580
            targetPort: 41580
        -   name: connect
            protocol: TCP
            port: 9000
            targetPort: 9000
---
apiVersion: apps/v1
kind: Deployment
metadata:
    name: coral-colony
spec:
    template:
        spec:
            containers:
                -   name: colony
                    image: coral/colony:latest
                    env:
                        -   name: CORAL_PUBLIC_ENDPOINT
                            value: "colony.example.com:41580"  # LoadBalancer hostname
                    ports:
                        -   containerPort: 41580
                            protocol: UDP
                        -   containerPort: 9000
                            protocol: TCP
```

**Behind NAT / Firewall:**

```bash
# 1. Configure port forwarding: external 41580/UDP → internal 41580/UDP
# 2. Use public IP in CORAL_PUBLIC_ENDPOINT
CORAL_PUBLIC_ENDPOINT=203.0.113.5:41580 coral colony start

# 3. Ensure firewall allows:
#    - Inbound UDP 41580 (WireGuard)
#    - Agents will connect via mesh, no additional ports needed
```

### Verifying Endpoint Configuration

After starting the colony, verify the endpoint is correctly advertised:

```bash
# Query discovery service
curl -X POST http://localhost:8080/coral.discovery.v1.DiscoveryService/LookupColony \
  -H "Content-Type: application/json" \
  -d '{"mesh_id": "<your-colony-id>"}'
```

Expected response:

```json
{
    "meshId": "my-app-prod",
    "pubkey": "...",
    "endpoints": [
        "203.0.113.5:41580"
    ],
    // ← Should be your public IP/hostname
    "meshIpv4": "10.42.0.1",
    "meshIpv6": "fd42::1",
    "connectPort": 9000
}
```

**✅ Correct**: `"endpoints": ["203.0.113.5:41580"]` or
`["colony.example.com:41580"]`
**❌ Wrong**: `"endpoints": ["10.42.0.1:41580"]` or `[":41580"]`

### Troubleshooting

#### Error: "No route to host" when agent connects

**Symptoms:**

```
Error: dial tcp [fd42::1]:9000: connect: no route to host
Error: dial tcp 10.42.0.1:9000: i/o timeout
```

**Cause:** WireGuard tunnel not established because endpoint is not reachable.

**Solution:**

1. Check `CORAL_PUBLIC_ENDPOINT` is set on colony
2. Verify endpoint is reachable from agent machine:
   ```bash
   nc -zvu <colony-ip> 41580
   ```
3. Check firewall allows UDP 41580

#### Error: "Endpoint is empty or invalid"

**Symptoms:**

```
WARN No WireGuard endpoints could be constructed
```

**Cause:** `CORAL_PUBLIC_ENDPOINT` is not set and defaulting to localhost.

**Solution:** Set `CORAL_PUBLIC_ENDPOINT` to your public IP or hostname.

#### Colony behind NAT

**Symptoms:** Agents can't connect from outside network.

**Solutions:**

1. **Port forwarding**: Forward UDP 41580 from router to colony machine
2. **UPnP/NAT-PMP** (future): Automatic port mapping
3. **STUN/TURN** (future): NAT traversal
4. **VPN tunnel**: Use external VPN to provide public IP

### Security Considerations

1. **Endpoint exposure**: Only the WireGuard port (41580/UDP) needs to be
   publicly accessible
2. **Service ports**: gRPC/Connect (9000) only accessible via WireGuard mesh
3. **Encryption**: All traffic encrypted by WireGuard (ChaCha20-Poly1305)
4. **Authentication**: Agent must have valid colony secret to join mesh

### Configuration Summary

| Environment    | CORAL_PUBLIC_ENDPOINT      | Usage                          |
|----------------|----------------------------|--------------------------------|
| **Local dev**  | (not set)                  | Default: `127.0.0.1:41580`     |
| **Production** | `203.0.113.5:41580`        | Public IP                      |
| **Production** | `colony.example.com:41580` | Hostname (recommended)         |
| **Docker**     | `<host-ip>:41580`          | Host machine IP                |
| **Kubernetes** | `<lb-hostname>:41580`      | LoadBalancer DNS               |
| **Behind NAT** | `<public-ip>:41580`        | External IP after port forward |

### Environment Variables Reference

```bash
# Required for production
CORAL_PUBLIC_ENDPOINT=colony.example.com:41580

# Optional overrides
CORAL_COLONY_ID=my-app-prod              # Colony to start
CORAL_DISCOVERY_ENDPOINT=http://...     # Discovery service URL
```

### Next Steps

- [Agent Deployment](./AGENT_DEPLOYMENT.md)
- [WireGuard Mesh Architecture](../RFDs/007-wireguard-mesh-implementation.md)
- [Discovery Service](../RFDs/001-discovery-service.md)
