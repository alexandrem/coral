# WireGuard Mesh Troubleshooting Guide

This guide covers common connectivity and state issues related to the WireGuard
mesh layer between Coral Agents and Colonies.

## Table of Contents

- [Mesh Diagnostic Commands](#mesh-diagnostic-commands)
    - [coral mesh ping](#coral-mesh-ping)
    - [coral mesh audit](#coral-mesh-audit)
- [Reading Audit Results](#reading-audit-results)
- [Common Issues](#common-issues)
    - [Symmetric NAT](#symmetric-nat)
    - [NAT and Firewall Packet Dropping](#nat-and-firewall-packet-dropping)
    - [Stale Handshakes](#stale-handshakes)
    - [Endpoint Configuration Mismatch](#endpoint-configuration-mismatch)

---

## Mesh Diagnostic Commands

Coral provides two dedicated commands for mesh troubleshooting. Both operate
entirely through user-space WireGuard and require no OS-level tools (`ping`,
`wg`, `tcpdump`) or elevated privileges beyond those already needed to run the
mesh.

### coral mesh ping

Sends encrypted UDP pings from the Colony to one or all agents through the
mesh and reports round-trip latency. This verifies the complete user-space
cryptography routing path independently of kernel ICMP filtering.

```bash
# Ping all connected agents
coral mesh ping

# Ping a specific agent (4 pings, 2s timeout each)
coral mesh ping prod-agent-01

# More pings with tighter timeout
coral mesh ping --count 10 --timeout 500ms
```

Example output:

```
MESH PING results via Colony (2 agents, 4 pings each):

AGENT ID                  MESH IP         LOSS       AVG RTT    STATUS
--------------------------------------------------------------------------------
prod-agent-01             10.255.0.2      0.0%       1.823ms    OK
prod-agent-02             10.255.0.3      100.0%     n/a        DEGRADED
```

A 100% loss with no RTT means Colony cannot reach the agent through the mesh.
Proceed to `coral mesh audit` to diagnose why.

---

### coral mesh audit

Compares Colony's **live WireGuard UAPI observations** against each agent's
**STUN-discovered endpoint at registration** to surface NAT issues without
any external tooling.

```bash
# Audit all connected agents
coral mesh audit

# Audit a specific agent
coral mesh audit prod-agent-01

# Machine-readable output for scripting or alerting
coral mesh audit --format json
coral mesh audit -o json | jq '.[] | select(.nat_type == "symmetric")'
```

Example output:

```
MESH AUDIT (2 agents)

AGENT                    MESH IP        COLONY OBSERVES         AGENT REGISTERED        NAT TYPE     HANDSHAKE    LINK
-------------------------------------------------------------------------------------------------------------------
prod-agent-01            10.255.0.2     203.0.113.42:51234      203.0.113.42:12345      SYMMETRIC !  45s ago      2.1MBup 1.0MBdn
prod-agent-02            10.255.0.3     10.0.1.5:51820          10.0.1.5:51820          direct       12s ago      512KBup 256KBdn

! prod-agent-01: SYMMETRIC NAT -- Colony sees port 51234 but agent's STUN reported 12345.
  WireGuard roaming handles this while traffic flows, but silence >180s may require agent restart.
```

JSON output example:

```json
[
  {
    "agentId": "prod-agent-01",
    "meshIp": "10.255.0.2",
    "colonyObservedEndpoint": "203.0.113.42:51234",
    "agentRegisteredEndpoint": "203.0.113.42:12345",
    "natType": "symmetric",
    "handshakeAgeSeconds": "45",
    "rxBytes": "1048576",
    "txBytes": "2097152"
  }
]
```

---

## Reading Audit Results

The `nat_type` field in `coral mesh audit` output classifies each agent's NAT
situation:

| NAT Type | Colony Observes | Agent Registered | Meaning |
|---|---|---|---|
| `direct` | `1.2.3.4:51820` | `1.2.3.4:51820` | Exact match. Cone NAT (port preserved) or public IP. Stable. |
| `symmetric` | `1.2.3.4:51234` | `1.2.3.4:12345` | Same IP, different port. Symmetric NAT: each destination gets a different mapping. |
| `roaming` | `1.2.3.4:51234` | *(empty)* | Agent registered without STUN. WireGuard roaming mode — Colony learns endpoint from incoming packets only. |
| `no_handshake` | *(empty)* | `1.2.3.4:51820` | No handshake ever completed. Agent never successfully sent a packet to Colony. |
| `unexpected` | `5.6.7.8:51234` | `1.2.3.4:51820` | Different IP. Possible double NAT, relay path, or carrier-grade NAT. |
| `error` | — | — | Agent is registered but not in the WireGuard peer list. Restart the colony. |

**Key columns:**

- **Colony Observes**: The source `IP:port` that Colony's WireGuard UAPI records
  as the last received packet from this agent. This is the *current* NAT mapping.
- **Agent Registered**: The `IP:port` the agent's STUN discovery reported at
  startup. This is what Colony *expected* the agent's address to be.
- **Handshake**: Age of the last successful WireGuard handshake. Handshakes
  older than 3 minutes indicate the peer is effectively offline.
- **Link**: Colony's received bytes (down) and sent bytes (up) for this peer.
  Asymmetry (high up, zero down) with a stale handshake confirms packet loss.

---

## Common Issues

### Symmetric NAT

**Detected by:** `coral mesh audit` reports `nat_type: symmetric`

**What it means:** The agent's NAT router assigns a different external port for
each remote destination. STUN saw one port when querying the STUN server; Colony
sees a different port when receiving packets.

**Impact:** WireGuard's built-in roaming handles this correctly as long as the
agent is actively sending traffic (the Colony learns the current mapping from
incoming packets). The risk is recovery after a long silence period (>180s):
WireGuard's dead-peer detection may time out before the agent resends.

**Solutions:**

1. **For most cases, no action needed.** The 25s `PersistentKeepalive` that
   Coral configures for every agent peer prevents the mapping from going stale
   during normal operation.
2. If agents repeatedly lose connectivity after network changes, add static
   UDP hole-punching rules on the NAT router, or consider deploying the Colony
   on a host with a publicly routable IP that the NAT router can reach directly.
3. If `coral mesh audit` shows `no_handshake` *and* `nat_type: symmetric`,
   the initial connection itself is being blocked — the NAT is restricting
   inbound packets from Colony to the agent before any session exists. A relay
   (TURN server) is required.

---

### NAT and Firewall Packet Dropping

**Detected by:** `coral mesh ping` shows 100% packet loss. `coral mesh audit`
shows a stale handshake with asymmetric Rx/Tx (high Tx, zero Rx).

Because WireGuard is connectionless (stateless UDP), the tunnel won't "crash"
or throw disconnection errors when traffic is blocked. The symptoms are subtle:

1. **Asymmetric Rx/Tx:** Colony transmits (`TxBytes` incrementing) but receives
   nothing (`RxBytes` at zero). Return UDP packets are dropped before reaching
   Colony.
2. **Stale handshake:** The handshake timestamp stops advancing past 3 minutes.
3. **Agent health degradation:** The `coral colony status` agent count shifts to
   degraded. gRPC health checks travelling inside the tunnel time out even though
   the WireGuard peer entry is still configured.

**Solutions:**

1. Verify that the Colony's WireGuard port is open for **UDP** traffic. Many
   cloud firewalls default to TCP-only rules.
2. Check if the agent host's firewall is blocking outbound UDP on the WireGuard
   port.
3. Run `coral mesh audit` to determine whether the issue is NAT-related (port
   mismatch) or a complete blockage (no handshake, zero Rx).

---

### Stale Handshakes

**Detected by:** `coral mesh audit` shows `handshake_age_seconds` > 180.

**Causes:**

1. The agent process exited or the host lost internet connectivity.
2. An intermediate firewall started blocking UDP on the WireGuard port.

**Solutions:**

1. Check agent logs on the remote machine (`journalctl -u coral-agent`).
2. WireGuard re-keys every ~2 minutes. If a handshake is older than 3 minutes,
   the peer is considered offline. When the agent comes back online it will
   restore the tunnel automatically.
3. If the agent is still running but the handshake is stale, use
   `coral mesh ping` to confirm whether Colony can reach the agent at all.

---

### Endpoint Configuration Mismatch

**Detected by:** `coral mesh audit` shows `nat_type: no_handshake` and
`agent_registered_endpoint` is empty or a private IP that is not reachable from
Colony.

**Causes:** The colony's `wireguard_endpoints` in its configuration contains a
private IP or local hostname that remote agents cannot resolve or reach.

**Solutions:**

1. Ensure `wireguard_endpoints` points to publicly routable IPs or correct DNS
   records that agents can reach over UDP.
2. Restart the colony to propagate the updated endpoints via the Discovery
   service.

---

## See Also

- **[Bootstrap Troubleshooting](BOOTSTRAP_TROUBLESHOOTING.md)**: Agent
  certificate bootstrap issues
- **[CLI Reference](CLI_REFERENCE.md)**: Full command reference including
  `coral mesh ping` and `coral mesh audit`
