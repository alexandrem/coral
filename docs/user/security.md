# Credentials and Access

Coral uses different credentials for server identity, agent enrollment, and
operator access. Obtain trust information from the Colony owner, separately
from an endpoint lookup.

| Credential | Purpose | Handling |
| --- | --- | --- |
| Root CA fingerprint | Verify the expected Colony during bootstrap or Discovery-based remote setup | Distribute through a trusted channel; a fingerprint from an untrusted lookup is not independent verification |
| Bootstrap PSK | Authorize a new agent to enroll | Keep secret; rotate when enrollment access changes |
| Agent certificate and private key | Represent the enrolled agent | Keep local with its matching enrollment and WireGuard state |
| Operator API token | Authenticate to the optional public HTTPS endpoint | Supply to the CLI or MCP process through its environment |
| LLM API key | Authenticate the operator's model provider | Configure locally; separate from mesh and Colony credentials |

## Provision an agent

On the Colony host, inspect the CA and prepare credentials:

```bash
coral colony ca status
coral colony export --format env
```

Export includes the active PSK. Send it only through your secret-provisioning
channel. Formats include `env`, `yaml`, `json`, and `k8s`; this is an enrollment
bundle, not a backup of the Colony's CA or database. It requires local access to
Colony configuration and state. Run state-management commands under the account
that owns that state; if another writer locks the database, stop that process
before the operation and restart it afterward.

The normal path is [agent startup](getting-started.md#enroll-and-start-an-agent),
which performs certificate and mesh enrollment. To request certificates
explicitly, use:

```bash
coral agent bootstrap \
  --colony '<colony-id>' \
  --fingerprint 'sha256:<root-ca-fingerprint>' \
  --psk 'coral-psk:<bootstrap-key>'
coral agent cert status
```

Run this as the intended agent account or specify the same `--certs-dir` used
by the service. Startup also needs matching committed enrollment state; a
successful standalone certificate request is not proof that an agent appears
in the mesh. Startup can perform fresh enrollment when that state is absent.

The fingerprint check verifies the Colony; the PSK authorizes initial
enrollment. Discovery locates endpoints and supplies referral information but
does not replace either trust check. A fingerprint or Colony ID mismatch is a
reason to verify the intended environment with its owner, not to skip
verification.

## Rotate enrollment credentials

```bash
coral colony psk rotate --grace-period 24h
coral colony psk show
```

Rotation moves the previous key into a grace period so provisioning can switch
to the new key. Distribute the new PSK before the grace period ends. Existing
agent certificates are unaffected: PSK rotation does not revoke already
enrolled agents or terminate their mesh access.

`coral colony ca rotate-intermediate --type agent --confirm` rotates an
intermediate CA. Back up CA state and understand the certificate lifecycle
before performing this administrative operation. Rotating an intermediate is
different from replacing the root trust anchor distributed to operators.

## Certificate expiry and restart state

```bash
coral agent cert status
coral agent cert renew --colony-endpoint 'https://<colony-host>:<bootstrap-port>'
```

Use the actual reachable TLS endpoint. Direct renewal uses the existing mTLS
identity. The presence of a renewal command does not imply a continuously
running background renewal worker; inspect status and startup logs. If a
certificate has expired, use fresh bootstrap with the currently trusted
fingerprint and PSK. See [recovery](troubleshooting.md#certificate-and-enrollment-recovery)
before replacing local state.

## Operator API tokens

First [enable the public endpoint](deployment.md#operator-https-access). On
the Colony host, create a token with the permissions required by the user:

```bash
coral colony token create operator --permissions status,query
coral colony token list
```

Save the token value when it is printed; it cannot be recovered later from the
token metadata. On the workstation:

```bash
export CORAL_API_TOKEN='<operator-token>'
coral query summary
```

Use `debug` for supported probe operations, `analyze` where the public API
requires it, and `admin` only for administrative access. These are the
implemented permission names, rather than generic `read` and `write` roles.
The [CLI manual](../CLI_REFERENCE.md) documents token options such as rate
limits. Revoke an unwanted token with:

```bash
coral colony token revoke operator
```

Token management changes the local token file. Send SIGHUP to the running Colony
process, or restart it, to reload the in-memory token store. Verify access with
the intended remaining token afterward. SIGHUP reloads tokens; it is not a general
hot reload of every server setting.

## Understand the access boundary

WireGuard encrypts traffic between mesh peers. Internal agent handlers also
have local and mesh HTTP/2 listeners; an issued certificate does not make every
handler require mTLS or implement per-command authorization. Restrict who can
reach internal listeners, especially those serving shell and container actions.

Shell requests include caller-supplied user labels and produce session metadata;
these labels are not verified operating-system identities or durable recordings
of terminal keystrokes. An AI client executes the same operational commands
available to its process. Review its access and model-provider data handling as
part of granting it investigation privileges. The [AI guide](ai-assistants.md)
explains where model calls occur.

For the precise trust and listener model, see
[identity and trust](../architecture/identity-and-trust.md) and
[remote execution](../architecture/remote-execution.md).
