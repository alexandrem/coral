# WireGuard Mesh Troubleshooting Guide

This guide covers common connectivity and state issues related to the WireGuard mesh layer between Coral Agents and Colonies.

## Table of Contents

- [Quick Diagnostics](#quick-diagnostics)
- [Common Issues](#common-issues)
  - [NAT and Firewall Packet Dropping](#nat-and-firewall-packet-dropping)
  - [Stale Handshakes](#stale-handshakes)
  - [Endpoint Configuration Mismatch](#endpoint-configuration-mismatch)

---

## Quick Diagnostics

You can quickly verify the real-time WireGuard mesh status natively using the Coral CLI.
Run these commands with the `--detail` flag to get a granular breakdown of your live peers.

```bash
# On the Colony Server
coral colony status --detail

# On the Agent
coral agent status --format json
```

Look specifically for the **Live Mesh Peers Information** block, which exposes raw Rx/Tx bytes and the last handshake timestamp.

---

## Common Issues

### NAT and Firewall Packet Dropping

Because WireGuard is connectionless (stateless UDP), the tunnel itself won't "crash" or explicitly throw immediate disconnection errors in the logs if traffic is blocked.

**Symptoms when inspecting `coral colony status --detail`:**

1. **Asymmetric Data Transfer (Rx/Tx Mismatch):** You will notice that `Tx Bytes` (Transmitted) incrementally increases as the colony relentlessly attempts to send handshake initiation packets. However, `Rx Bytes` (Received) will remain completely stagnant (or if `0`, hidden entirely), proving that return UDP packets from the agent are being dropped before they reach the colony.
2. **Stale or Missing Handshakes:** The `Handshake` property will either be completely missing (if strictly blocked from the start) or stale (older than 3 minutes).
3. **Agent Health Degradation:** In the `Runtime Status` block, the agent count will shift to degraded: `Agents: 2 connected (✓1 ⚠1)`. Even though the WireGuard peer is technically "configured," the gRPC health checks traveling _inside_ the tunnel will time out.

**Solutions:**

1. Verify that your Colony's WireGuard port is exposed to the internet/network for **UDP** traffic. Many cloud provider firewalls default to only exposing TCP.
2. Check if the Agent is behind a strict NAT. Coral automatically utilizes `PersistentKeepalive` to traverse NATs by keeping the firewall translation mapping alive, but restrictive symmetric NATs may still require specific UDP hole-punching or firewall bypass rules to reach the Colony.

---

### Stale Handshakes

**Symptom:**

```
  Connected Peers: 1
    - ZqT+hW/0mu9n...
        Endpoint:   <agent-ip>:54123
        Rx Bytes:   4502123
        Tx Bytes:   10234123
        Handshake:  15 minutes ago
```

**Causes:**

1. The agent process unexpectedly died or the host machine lost internet connectivity.
2. An intermediate firewall abruptly started dropping UDP packets on the WireGuard port.

**Solutions:**

1. Check the agent logs on the remote machine (`journalctl -u coral-agent`).
2. WireGuard re-keys its session perfectly every ~2 minutes. If a handshake is older than 3 minutes, the peer is offline. If an agent comes back online, it should seamlessly restore the tunnel.

---

### Endpoint Configuration Mismatch

**Symptom:**
The agent is unable to form a tunnel, and the discovery logs show it failing to reach the colony mesh.

**Causes:**
The colony configured `WireGuardEndpoints` using a private IP or local hostname that the remote agent cannot resolve from its external network.

**Solutions:**

1. Ensure the `wireguard_endpoints` list in your colony's configuration points to publicly routable IPs or correct DNS records that the agent can resolve and reach over UDP.
2. Restart the colony to propagate the updated endpoints via the Discovery service.

---

## See Also

- **[Bootstrap Troubleshooting](BOOTSTRAP_TROUBLESHOOTING.md)**: Agent certificate bootstrap issues
- **[CLI Reference](CLI_REFERENCE.md)**: Available commands
