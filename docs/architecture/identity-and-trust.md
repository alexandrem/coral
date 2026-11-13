# Identity and Trust

Coral separates three questions: where a peer can be reached, which Colony or
agent it represents, and whether it may enroll. WireGuard endpoints answer the
first question. Certificates and SPIFFE identities answer the second. Referral
tickets and Bootstrap PSK validation participate in the third. Discovery's
endpoint response alone does not authorize a peer.

## Colony-owned certificate authority

The Colony's CA manager maintains a three-level hierarchy: a Root CA signs
server and agent intermediates, which sign operational certificates. A separate
policy-signing certificate supports signed policy material. The implementation
does not create a fourth level or a dedicated bootstrap intermediate.

The Root CA is the trust anchor an operator distributes as a SHA-256
fingerprint. The server intermediate signs the Colony's TLS identity; the agent
intermediate signs agent client identities. The CA stores key material on disk
and issuance/revocation records in the Colony database. These have different
recovery roles: an issuance record does not replace the private signing key.

Configured certificate validity comes from `PolicyEnforcer.GetCertificateValidity`:
Root and policy-signing certificates use ten years, intermediates one year, and
agent and server leaf certificates 90 days. These are validity defaults, not a
promise that a running process rotates every credential automatically.

Implementation: [CA manager](../../internal/colony/ca/manager.go),
[signing operations](../../internal/colony/ca/crypto_ops.go), and
[policy](../../internal/colony/ca/policy.go).

## Identities inside certificates

A Colony certificate carries `spiffe://coral/colony/{colony-id}`. An agent
certificate carries `spiffe://coral/colony/{colony-id}/agent/{agent-id}`. The
Colony ID is therefore part of the identity, rather than context supplied by a
caller after authentication.

The agent creates its own Ed25519 key and a CSR. The Colony validates the CSR
signature and checks its Common Name against
`agent.{agent-id}.{colony-id}` before signing. The certificate's public key comes
from that CSR; the agent's private key stays local.

## First contact

Before an agent has a mesh path, it connects to the Colony's bootstrap TLS
endpoint. `CAValidator` checks the presented chain, compares the Root CA
fingerprint with the operator's configured value, and validates the Colony's
SPIFFE identity. Supplying an endpoint through Discovery changes where the
agent dials; it does not change the expected trust anchor.

The agent then sends its CSR and enrollment credentials over that connection.
The Colony validates the referral ticket using Discovery's JWKS and checks the
requested identity. Non-renewal enrollment also requires the Bootstrap PSK.
This shared secret is an explicit part of initial authorization, alongside
certificate-based server verification.

Reverse-dial rendezvous changes which peer opens TCP, while keeping the Colony
as TLS server and the agent as TLS client. The same fingerprint and SPIFFE
checks apply. [Discovery and enrollment](discovery-and-enrollment.md) owns the
rendezvous protocol and the durable registration state machine.

Implementation: [bootstrap client](../../internal/agent/bootstrap/client.go),
[CA validation](../../internal/agent/bootstrap/ca_validator.go),
[certificate request handler](../../internal/colony/server/server.go), and
[compound enroller](../../internal/colony/enrollment/enroller.go).

## Transport boundaries after bootstrap

The existence of an agent certificate does not imply that every RPC requires
mTLS. The agent's internal HTTP/2 handlers use h2c on mesh and localhost
listeners. Mesh traffic receives WireGuard encryption; localhost and externally
configured listeners have their own reachability and authentication boundaries.
The Colony's optional HTTP API has separate TLS and authentication configuration.

Consequently, do not infer authorization for an internal handler from the CA
hierarchy alone. Inspect the listener and middleware serving that handler.
Likewise, `RevokeCertificate` records revocation in the CA database; it does not
by itself remove a WireGuard peer or terminate all existing sessions. A claim
of immediate fleet-wide exclusion would require those enforcement paths too.

Implementation: [agent listeners](../../internal/cli/agent/startup/services.go),
[Colony server setup](../../internal/cli/colony/server.go),
[HTTP API](../../internal/colony/httpapi/server.go), and
[CA persistence](../../internal/colony/ca/storage.go).

## Credential and restart lifecycle

The certificate manager loads the keypair and CA chain, exposes a TLS client
configuration, and classifies expiry. Its `RenewalThreshold` is 30 days before
expiry. The bootstrap client has a `Renew` method, but that method and the
expiry classification do not establish a continuously running renewal worker.
Agent startup handles existing, expiring, and expired credentials in its
bootstrap phase; describe that path rather than assuming rotation at a fixed
percentage of certificate lifetime.

Startup also requires a matching committed enrollment checkpoint to reuse an
existing certificate as a usable mesh identity. The local checkpoint binds the
certificate hash to the enrolled agent, Colony, WireGuard identity, and mesh
assignment. If that state is missing or inconsistent, startup performs fresh
compound enrollment. A certificate file alone is insufficient evidence that
mesh registration completed. On the rendezvous path, fresh enrollment uses the
compound operation described above.

Implementation: [certificate manager](../../internal/agent/certs/manager.go),
[startup bootstrap](../../internal/cli/agent/startup/bootstrap.go), and
[local enrollment store](../../internal/agent/enrollmentstate/store.go).
Operational setup is documented in [Bootstrap security](../user/security.md).

## Design history

- [RFD 047: Colony CA Infrastructure](../../RFDs/047-colony-ca-infrastructure.md)
- [RFD 048: Agent Certificate Bootstrap](../../RFDs/048-agent-certificate-bootstrap.md)
- [RFD 088: Bootstrap PSK](../../RFDs/088-bootstrap-psk.md)
- [RFD 108: PSK Rendezvous](../../RFDs/108-psk-rendezvous-agent-bootstrap.md)
- [RFD 109: Compound Enrollment](../../RFDs/109-rendezvous-mesh-enrollment.md)
