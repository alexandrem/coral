# Installation & Permissions

Coral creates a WireGuard mesh network for secure communication between colony
and agents. This requires elevated privileges for TUN device creation.

**Choose one installation method:**

## Option 1: Linux Capabilities (Recommended)

Grant only the `CAP_NET_ADMIN` capability to the binary:

```bash
sudo setcap cap_net_admin+ep ./bin/coral
```

**Why this is preferred:**

- Only grants the specific permission needed (network administration)
- Process runs as your regular user (not root)
- No password prompts after initial setup
- Most secure option (Linux only)

## Option 2: Run with sudo

Run Coral with sudo when starting the colony:

```bash
sudo ./bin/coral colony start
```

**Trade-offs:**

- ✅ Coral automatically preserves file ownership (configs stay user-owned)
- ⚠️ Entire colony process initially runs as root
- ⚠️ Requires password entry on each start
- Works on all platforms (Linux, macOS)

> **Note:** While the whole process starts as root, Coral detects `SUDO_USER`
> and ensures all config files in `~/.coral/` remain owned by your regular user
> account.

## Option 3: Setuid Binary (Convenience vs. Security)

**Security: ⭐ Use with caution** | **UX: ⭐⭐⭐⭐⭐ Seamless**

Make the binary setuid root:

```bash
sudo chown root:root ./bin/coral
sudo chmod u+s ./bin/coral
```

**Trade-offs:**

- ✅ No password prompts, seamless experience
- ✅ Config files remain user-owned
- ⚠️ Any vulnerability in the binary could be exploited for privilege escalation
- ⚠️ All users on the system can run it with elevated privileges
- ⚠️ Only recommended for single-user development machines

> **Future Enhancement:** A privileged helper subprocess approach is in
> development (see [RFD 008](RFDs/008-privilege-separation.md)) which will
> provide the UX of Option 3 with security closer to Option 1. The helper will
> spawn only for TUN creation, minimizing the privilege window.
