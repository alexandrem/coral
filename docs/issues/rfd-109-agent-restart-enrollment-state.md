# RFD 109 Agent Restart Loses Mesh Enrollment State

Status: Proposed  
Affected areas: Agent startup, certificate storage, WireGuard identity, RFD 109 enrollment  
Related documents: RFD 048, RFD 108, RFD 109

## Summary

An Agent can complete RFD 109 compound certificate bootstrap and mesh
registration, then fail later in startup. The certificate remains on disk, but
the returned mesh registration and the Agent WireGuard key are not persisted.
On restart, the valid certificate suppresses compound bootstrap, a new
WireGuard key is generated, and the Agent attempts ordinary pre-mesh
registration. That registration cannot reach a NAT-local Colony.

Operators currently recover by deleting the Agent certificate directory. This
forces compound bootstrap to run again, but it discards a valid identity and
hides the underlying restart-state bug.

The Agent must persist certificate identity, WireGuard identity, and mesh
assignment as one versioned enrollment checkpoint. A restart must either
restore a complete checkpoint or automatically resume compound enrollment. A
certificate alone must not be treated as proof that mesh enrollment is locally
usable.

## User-visible behavior

A typical failure sequence is:

1. The Agent registers its observed WireGuard endpoint with Discovery.
2. The Colony handles `BootstrapAndRegister`, installs the Agent peer, issues a
   certificate, and returns the assigned mesh address.
3. The Agent writes the certificate files.
4. Agent startup fails or the process exits before mesh configuration is fully
   applied.
5. On restart, startup logs that it is using an existing valid certificate.
6. The Agent generates a different WireGuard key and has no assigned IP or
   subnet from the previous response.
7. Ordinary Colony registration retries indefinitely because the Colony is not
   directly dialable before the WireGuard tunnel exists.
8. Removing the certificate directory makes the next startup succeed because
   it forces `BootstrapAndRegister` to run again.

This can also occur after an apparently successful run because WireGuard keys
are currently regenerated on every process start while the registration
assignment is held only in memory.

## Root cause

The startup code treats two different states as though they were equivalent:

- A valid mTLS certificate exists.
- RFD 109 certificate and mesh enrollment completed and can be restored.

They are not equivalent.

`BootstrapPhase.Execute` returns early when it loads a valid certificate. The
returned `BootstrapResult` has no compound `Registration` response. The only
path that applies the RFD 109 assignment checks that in-memory response. With
no response, startup falls through to ordinary `MeshService.Register`.

Separately, `NetworkInitializer` generates a new WireGuard keypair each time.
The Colony may therefore retain a peer for the key used by the previous
compound enrollment while the restarted Agent advertises a new key.

Certificate reuse also does not require the certificate's Agent ID and Colony
ID to match the identity selected for the current startup. The default shared
certificate directory can consequently contain a valid but unrelated
certificate.

## Required invariants

The fix must maintain these invariants:

1. A successfully enrolled Agent has a stable WireGuard identity across
   restarts.
2. A certificate is reused only when its Agent ID and Colony ID match the
   current startup identity.
3. The certificate, WireGuard public key, assigned mesh IP, and mesh subnet
   belong to the same completed enrollment.
4. Startup never interprets partially written local state as a completed
   enrollment.
5. A crash at any local write or startup boundary is automatically recoverable.
6. Discovery remains the source of live public endpoints. Persisted state must
   not turn a STUN-observed address into a static endpoint.
7. Private keys remain readable only by the Agent owner.
8. Recovery does not require an operator to delete certificate files.

## Proposed design

### 1. Add a versioned Agent enrollment checkpoint

Store a checkpoint beneath the configured certificate directory. A suggested
logical schema is:

```json
{
  "schema_version": 1,
  "state": "enrolled",
  "agent_id": "vmi3425109",
  "colony_id": "showup-local-colony-dev-e37b12",
  "wireguard_private_key": "<base64>",
  "wireguard_public_key": "<base64>",
  "assigned_ip": "100.64.0.5",
  "mesh_subnet": "100.64.0.0/10",
  "certificate_sha256": "<hex>",
  "certificate_serial": "<decimal>",
  "enrolled_at": "2026-08-10T22:27:30Z"
}
```

The exact on-disk encoding is an implementation detail. The store should expose
typed operations such as `Load`, `SavePendingIdentity`, `CommitEnrollment`,
`ValidateAgainstCertificate`, and `ArchiveIncomplete`.

Do not store the Colony or Agent public network endpoint in this checkpoint.
Those endpoints can change between environments and restarts and must continue
to come from STUN and Discovery.

### 2. Persist the WireGuard identity before advertising it

Network initialization should load the existing WireGuard key from the
checkpoint. If none exists, it should generate a key and durably save a
`pending` identity before registering the public key or STUN endpoint with
Discovery.

Reusing a pending key makes a retry stable even if the process exits before
compound enrollment completes. Once enrollment succeeds, the same key becomes
part of the committed checkpoint.

The private key file and checkpoint must use mode `0600`; the containing
directory must use mode `0700`. Ownership correction must follow the existing
certificate-storage behavior when startup runs with elevated privileges.

### 3. Commit local enrollment atomically

After `BootstrapAndRegister` succeeds, write the certificate material and mesh
assignment using an atomic commit protocol:

1. Validate that the returned certificate contains the expected Agent and
   Colony SPIFFE identity.
2. Validate that the registration response is complete and that the checkpoint
   WireGuard public key equals the key sent in the registration request.
3. Write certificate and state files to temporary files in the destination
   directory.
4. Set their final permissions and synchronize file contents.
5. Rename each file into place.
6. Write and atomically rename the `enrolled` manifest last as the commit
   marker.
7. Synchronize the containing directory where supported.

Startup may trust the enrollment only when the final manifest exists and all
hashes and identities validate. Certificate files without that manifest are an
incomplete enrollment, not a completed bootstrap.

### 4. Restore before deciding whether to bootstrap

Startup ordering should become:

1. Resolve the current Agent ID and Colony ID.
2. Load and validate the enrollment checkpoint.
3. Restore or generate the stable WireGuard key.
4. Start WireGuard on the configured fixed port, perform STUN, and register the
   current observed endpoint and persisted public key with Discovery.
5. If the checkpoint is complete, load the certificate, refresh Colony
   information from Discovery, and apply the persisted IP and subnet. Configure
   the Colony peer using the current Discovery data or endpoint-less WireGuard
   roaming as appropriate.
6. If the checkpoint is absent or incomplete, run compound enrollment.

This removes the current assumption that a certificate check can independently
decide whether compound enrollment is needed.

### 5. Recover certificate-only and mismatched state automatically

Existing installations may contain valid certificate files but no checkpoint.
Treat this as legacy or interrupted state:

- Log a structured `agent_enrollment_state_incomplete` event.
- Move the old files into a timestamped recovery directory instead of deleting
  them.
- Keep or create a stable pending WireGuard key.
- Run a fresh `BootstrapAndRegister` using a fresh referral ticket.
- Atomically commit the resulting bundle.

This is the compatibility-first recovery path and requires no Discovery API
change. The Colony's existing RFD 109 peer-replacement phases remove the old
WireGuard peer and install the new or recovered key.

If a loaded certificate names another Agent or Colony, do not silently reuse
it. Archive it, emit an identity-mismatch event, and bootstrap the selected
identity. A later improvement may namespace default credentials by
`<colony-id>/<agent-id>` to avoid collisions entirely.

### 6. Optional follow-up: registration-only rendezvous recovery

Reissuing a certificate is unnecessary when the existing certificate is valid
and only the local mesh assignment is missing. A follow-up protocol could add
an authenticated registration-only operation over the RFD 108 rendezvous
connection. The Agent would prove possession of its existing certificate and
request restoration or rotation of its WireGuard peer and mesh assignment.

This should not block the initial fix. Automatic compound re-enrollment is
safer and simpler than continuing to require manual certificate deletion.

## Failure handling

| Local state | Startup behavior |
| --- | --- |
| No certificate and no checkpoint | Create pending WireGuard identity and run compound enrollment. |
| Pending WireGuard identity only | Reuse the key and retry compound enrollment. |
| Certificate files without committed checkpoint | Archive incomplete state and automatically re-enroll. |
| Complete checkpoint and matching valid certificate | Restore WireGuard key, IP, and subnet; do not reissue the certificate. |
| Complete checkpoint with corrupt or missing files | Archive the bundle and automatically re-enroll. |
| Certificate Agent/Colony identity mismatch | Refuse reuse, archive it, and enroll the requested identity. |
| Expired certificate with valid mesh checkpoint | Preserve the stable WireGuard identity and renew or re-bootstrap the certificate. |
| Discovery endpoint changed | Keep local identity and assignment; consume the newly discovered endpoint dynamically. |

## Observability

Add structured events with Agent ID, Colony ID, checkpoint version, and failure
class, but never private-key or certificate contents:

- `agent_enrollment_checkpoint_loaded`
- `agent_enrollment_checkpoint_committed`
- `agent_enrollment_checkpoint_invalid`
- `agent_enrollment_state_incomplete`
- `agent_enrollment_identity_mismatch`
- `agent_enrollment_recovery_started`
- `agent_enrollment_recovery_completed`

The startup summary should distinguish `certificate_loaded`,
`mesh_enrollment_restored`, and `compound_enrollment_completed`. A generic
"using existing valid certificate" message is insufficient when mesh state is
missing.

## Test plan

### Unit tests

- Checkpoint round trip preserves the WireGuard key and assignment.
- Private state is created with mode `0600` and its directory with `0700`.
- A manifest is not considered committed until the final atomic rename.
- Certificate hash, Agent ID, Colony ID, SPIFFE ID, and WireGuard public key
  mismatches are rejected.
- Corrupt, truncated, missing, and unsupported-version checkpoints produce a
  recoverable incomplete-state result.
- A pending WireGuard key is reused across retries.
- Live Discovery endpoints are never loaded from the checkpoint.

### Startup integration tests

Inject process failure at each boundary:

1. After pending WireGuard identity persistence.
2. After Colony `BootstrapAndRegister` completion but before local certificate
   writes.
3. After certificate writes but before checkpoint commit.
4. After checkpoint commit but before WireGuard mesh configuration.
5. After mesh configuration but before the Agent reports startup success.

Every restart must succeed without deleting files. Once a checkpoint is
committed, the Agent must reuse the same WireGuard public key and assigned mesh
IP and must not issue another certificate.

### RFD 109 end-to-end test

Extend the NAT-local Colony rendezvous test to:

1. Complete compound enrollment.
2. Stop the Agent.
3. Restart it with the same state directory.
4. Assert that the WireGuard public key and assigned IP are unchanged.
5. Assert that no second certificate is issued.
6. Assert that the Agent reconnects using the Colony endpoint currently
   returned by Discovery.
7. Change the Discovery-advertised endpoint and repeat the restart to prove no
   static endpoint was persisted.

Add a second case that terminates the first Agent after certificate files are
written but before the checkpoint commit. The next process must recover through
compound enrollment automatically and leave exactly one Colony peer.

## Acceptance criteria

- Restarting an enrolled Agent never requires deleting its certificate
  directory.
- A failed startup after certificate issuance recovers automatically on the
  next start.
- The Agent uses the same WireGuard key and assigned mesh IP after an ordinary
  restart.
- A valid certificate cannot suppress required mesh enrollment when its local
  checkpoint is absent or invalid.
- Credentials for another Agent or Colony are never silently reused.
- Colony and Agent public endpoints remain dynamically sourced from Discovery.
- RFD 109 restart and crash-injection end-to-end tests pass.

## Alternatives considered

### Always force certificate bootstrap on startup

This masks the missing state but unnecessarily reissues certificates, consumes
referral tickets, and rotates Colony peers on every restart.

### Persist only the registration response

This still generates a new WireGuard key after restart, so the persisted
assignment and Colony peer no longer describe the active Agent identity.

### Treat certificate files as the enrollment marker

This is the current bug. Certificate issuance can complete before local mesh
configuration, and certificate files contain neither the WireGuard key nor the
assigned subnet.

### Persist the STUN-observed endpoint

Public mappings can change after a restart or environment change. Persisting
them would undermine Discovery and recreate the static-configuration problem.

## Implementation outline

1. Add an `internal/agent/enrollmentstate` package with versioned, atomic,
   permission-safe storage.
2. Change network initialization to load or create the persisted WireGuard
   identity instead of always generating one.
3. Bind certificate reuse to the current Agent ID, Colony ID, and committed
   enrollment manifest.
4. Commit certificate and mesh state as one logical transaction after
   `BootstrapAndRegister` succeeds.
5. Restore the assignment before ordinary Colony registration begins.
6. Add automatic migration/recovery for certificate-only directories.
7. Add unit, restart-integration, crash-injection, and NAT-local end-to-end
   coverage.
8. Update RFD 109 and Agent troubleshooting documentation after implementation.
