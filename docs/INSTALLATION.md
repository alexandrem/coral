# Installation & Permissions

Coral requires elevated privileges for WireGuard mesh networking and eBPF observability.

> **📖 Detailed explanation:** See [PRIVILEGE.md](PRIVILEGE.md) for architecture, security model, and graceful degradation.

## Quick Setup

### Option 1: Linux Capabilities (Recommended)

**One-time setup:**
```bash
# Colony (needs only CAP_NET_ADMIN)
sudo setcap 'cap_net_admin+ep' /path/to/coral

# Agent (needs additional capabilities for eBPF)
sudo setcap 'cap_net_admin,cap_sys_admin,cap_sys_ptrace,cap_sys_resource,cap_bpf+ep' /path/to/coral
```

**Run without sudo:**
```bash
coral colony start  # Only needs CAP_NET_ADMIN
coral agent start   # Needs all capabilities above
```

✅ Most secure • Least privilege • No password prompts • Linux only

---

### Option 2: Run with sudo (All Platforms)

**Every time:**
```bash
sudo coral colony start
sudo coral agent start
```

⚠️ Password prompt each time • Works on macOS

---

### Option 3: Setuid (Not Recommended)

**One-time setup:**
```bash
sudo chown root:root /path/to/coral
sudo chmod u+s /path/to/coral
```

**Run directly:**
```bash
coral colony start
```

🚨 **Security risk** • Any user gets root • Development only

---

## No Privileges Needed

```bash
coral proxy start <colony-id>  # No sudo required
```

The proxy command is just HTTP forwarding and doesn't need elevated privileges.
