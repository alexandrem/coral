# Bootstrap Troubleshooting Guide

This guide covers common issues and solutions for agent certificate bootstrap.

## Table of Contents

- [Quick Diagnostics](#quick-diagnostics)
- [Common Issues](#common-issues)
    - [CA Fingerprint Mismatch](#ca-fingerprint-mismatch)
    - [Colony ID Mismatch](#colony-id-mismatch)
    - [Discovery Connection Failed](#discovery-connection-failed)
    - [Certificate Expired](#certificate-expired)
    - [Permission Denied](#permission-denied)
    - [Timeout During Bootstrap](#timeout-during-bootstrap)
- [Advanced Diagnostics](#advanced-diagnostics)
- [Recovery Procedures](#recovery-procedures)

---

## Quick Diagnostics

Run these commands to quickly diagnose bootstrap issues:

```bash
# Check certificate status
coral agent cert status

# Verify certificate files exist
ls -la ~/.coral/certs/

# Check certificate details
openssl x509 -in ~/.coral/certs/agent.crt -text -noout

# Verify CA fingerprint
openssl x509 -in ~/.coral/certs/root-ca.crt -fingerprint -sha256 -noout

# Test connectivity to Discovery
curl -v https://discovery.coralmesh.dev/health

# Test connectivity to Colony (requires valid cert)
curl -v --cert ~/.coral/certs/agent.crt --key ~/.coral/certs/agent.key \
  https://colony.example.com:9000/health
```

---

## Common Issues

### CA Fingerprint Mismatch

**Symptom:**

```
Error: CA fingerprint mismatch - potential MITM attack detected
  Expected: sha256:abc123...
  Received: sha256:def456...
```

**Causes:**

1. Wrong fingerprint configured
2. Colony CA was rotated
3. Actual MITM attack
4. Connecting to wrong colony

**Solutions:**

1. **Verify fingerprint**: Get the correct fingerprint from your colony admin:

   ```bash
   # On colony server
   coral colony ca status
   # Output: Root CA fingerprint: sha256:abc123...
   ```

2. **Update agent configuration**:

   ```yaml
   agent:
     bootstrap:
       ca_fingerprint: "sha256:<correct-fingerprint>"
   ```

   Or via environment variable:

   ```bash
   export CORAL_CA_FINGERPRINT="sha256:<correct-fingerprint>"
   ```

3. **If CA was rotated**: Obtain new fingerprint from colony admin and update
   all agents.

4. **If MITM suspected**: Do NOT proceed. Verify network path to colony is
   secure. Contact security team.

---

### Colony ID Mismatch

**Symptom:**

```
Error: Colony ID mismatch - potential cross-colony impersonation
  Expected: my-app-prod
  Received: attacker-colony
```

**Causes:**

1. Wrong colony ID configured
2. DNS hijacking or misconfiguration
3. Cross-colony impersonation attempt

**Solutions:**

1. **Verify colony ID**: Check your agent configuration:

   ```yaml
   agent:
     colony:
       id: "my-app-prod"  # Must match colony's actual ID
   ```

2. **Verify DNS**: Ensure Discovery returns the correct colony endpoint:

   ```bash
   curl https://discovery.coral.:8080/v1/colonies/my-app-prod
   ```

3. **Check network**: Verify you're connecting to the intended colony server.

---

### Discovery Connection Failed

**Symptom:**

```
Error: failed to connect to Discovery service: connection refused
```

or

```
Error: failed to get referral ticket: context deadline exceeded
```

**Causes:**

1. Discovery service is down
2. Network connectivity issues
3. Firewall blocking connection
4. Wrong Discovery endpoint configured

**Solutions:**

1. **Verify Discovery is reachable**:

   ```bash
   curl -v https://discovery.coralmesh.dev/health
   ```

2. **Check firewall rules**: Ensure outbound HTTPS (port 443 or 8080) is
   allowed.

3. **Verify endpoint configuration**:

   ```yaml
   # In ~/.coral/config.yaml
   discovery:
     endpoint: "https://discovery.coralmesh.dev"
   ```

   Or via environment variable:

   ```bash
   export CORAL_DISCOVERY_ENDPOINT="https://discovery.coralmesh.dev"
   ```

---

### Certificate Expired

**Symptom:**

```
Error: certificate has expired
  Not After: 2025-01-01T00:00:00Z
  Current:   2025-01-15T10:00:00Z
```

or

```
coral agent cert status
# Status: ✗ Expired
```

**Causes:**

1. Automatic renewal failed
2. Agent was offline during renewal window
3. Colony was unreachable during renewal

**Solutions:**

1. **Full bootstrap required**: Expired certificates cannot be renewed. You must
   perform a fresh bootstrap:

   ```bash
   # Remove expired certificate
   rm ~/.coral/certs/agent.crt ~/.coral/certs/agent.key

   # Re-bootstrap
   coral agent bootstrap \
     --colony my-app-prod \
     --fingerprint sha256:abc123...
   ```

2. **Prevent future expiry**: Ensure the agent can reach the colony for renewal.
   Certificate renewal happens automatically when the certificate has < 30 days
   remaining.

---

### Permission Denied

**Symptom:**

```
Error: failed to save certificate: permission denied
  Path: /etc/coral/certs/agent.key
```

**Causes:**

1. Certificate directory not writable
2. Running as wrong user
3. SELinux/AppArmor blocking writes

**Solutions:**

1. **Check directory permissions**:

   ```bash
   ls -la /etc/coral/certs/
   # Should be writable by agent user
   ```

2. **Fix permissions**:

   ```bash
   sudo mkdir -p /etc/coral/certs
   sudo chown $(whoami):$(whoami) /etc/coral/certs
   chmod 700 /etc/coral/certs
   ```

3. **Use default directory**: If custom path has issues, use the default:

   ```bash
   # Default: ~/.coral/certs/
   unset CORAL_CERTS_DIR
   ```

4. **Check SELinux/AppArmor**: If using custom paths, you may need to update
   security policies.

---

### Timeout During Bootstrap

**Symptom:**

```
Error: bootstrap failed: context deadline exceeded
```

**Causes:**

1. Network latency
2. Colony overloaded
3. Discovery slow to respond
4. Retry attempts exhausted

**Solutions:**

1. **Increase timeout**:

   ```yaml
   agent:
     bootstrap:
       total_timeout: 10m  # Default: 5m
       retry_attempts: 20  # Default: 10
       retry_delay: 2s     # Default: 1s
   ```

2. **Check network latency**:

   ```bash
   ping discovery.coralmesh.dev
   ping colony.example.com
   ```

3. **Verify services are healthy**:

   ```bash
   curl https://discovery.coralmesh.dev/health
   curl https://colony.example.com:9000/health
   ```

4. **Retry manually**:

   ```bash
   coral agent bootstrap --colony my-app-prod --fingerprint sha256:abc123...
   ```

---

## Advanced Diagnostics

### Enable Debug Logging

```bash
# Set log level to debug
export CORAL_LOG_LEVEL=debug

# Run bootstrap with verbose output
coral agent bootstrap --colony my-app-prod --fingerprint sha256:abc123...
```

### Inspect Certificate Chain

```bash
# View full certificate chain
openssl crl2pkcs7 -nocrl -certfile ~/.coral/certs/ca-chain.crt | \
  openssl pkcs7 -print_certs -text -noout

# Verify certificate chain
openssl verify -CAfile ~/.coral/certs/root-ca.crt \
  -untrusted ~/.coral/certs/ca-chain.crt \
  ~/.coral/certs/agent.crt
```

### Check SPIFFE ID

```bash
# Extract SPIFFE ID from certificate
openssl x509 -in ~/.coral/certs/agent.crt -text -noout | grep -A1 "URI:"

# Expected format:
# URI:spiffe://coral/colony/<colony-id>/agent/<agent-id>
```

### Test mTLS Connection

```bash
# Test mTLS to colony
openssl s_client -connect colony.example.com:9000 \
  -cert ~/.coral/certs/agent.crt \
  -key ~/.coral/certs/agent.key \
  -CAfile ~/.coral/certs/root-ca.crt
```

### Check Telemetry Metrics

The agent records bootstrap metrics for monitoring:

```bash
# View bootstrap attempt logs
grep "coral_agent_bootstrap_attempt" /var/log/coral-agent.log

# Metrics recorded:
# - result: success|failure|timeout|fallback
# - duration_seconds: time taken
# - agent_id: agent identifier
# - colony_id: target colony
```

---

## Recovery Procedures

### Complete Certificate Reset

If bootstrap is stuck in a bad state:

```bash
# 1. Stop the agent
systemctl stop coral-agent

# 2. Backup and remove old certificates
mv ~/.coral/certs ~/.coral/certs.backup.$(date +%Y%m%d)

# 3. Verify configuration
cat ~/.coral/config.yaml

# 4. Re-bootstrap
coral agent bootstrap \
  --colony my-app-prod \
  --fingerprint sha256:abc123...

# 5. Verify new certificate
coral agent cert status

# 6. Restart agent
systemctl start coral-agent
```

---

## Getting Help

If issues persist:

1. **Collect diagnostics**:

   ```bash
   coral agent cert status > diag.txt
   ls -la ~/.coral/certs/ >> diag.txt
   cat ~/.coral/config.yaml >> diag.txt  # Remove secrets!
   ```

2. **Check logs**:

   ```bash
   journalctl -u coral-agent --since "1 hour ago" > agent-logs.txt
   ```

3. **Report issue**: Open an issue
   at https://github.com/alexandrem/coral/issues
   with diagnostics (remove any secrets/sensitive data).

---

## See Also

- **[Agent Documentation](AGENT.md)**: Full agent documentation
- **[Configuration Guide](CONFIG.md)**: Configuration options
- **[Security Guide](SECURITY.md)**: Security best practices
