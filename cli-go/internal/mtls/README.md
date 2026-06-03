# internal/mtls — mutual TLS certificate management (Q2 override)

`mtls` implements self-signed CA generation and mutual TLS certificate
issuance for cross-machine yakOS daemon connections (Phase 2, Q2 override).

## Design

```
~/.yakos-state/mtls/
├── ca.crt          (CA certificate, mode 0600)
├── ca.key          (CA private key, mode 0600)
└── clients/
    ├── <name>.crt  (client cert, mode 0600)
    └── <name>.key  (client key, mode 0600)
```

- CA: RSA-2048, self-signed, 10-year validity.
- Server cert: signed by CA, 1-year validity, `ExtKeyUsageServerAuth`.
  Always includes `127.0.0.1` and `::1` SANs so loopback tests work.
- Client cert: signed by CA, 1-year validity, `ExtKeyUsageClientAuth`, CN = client name.

All files are written atomically (temp + rename) at mode 0600.

## Key functions

| Function | Description |
|----------|-------------|
| `LoadOrGenerateCA(stateDir)` | Load or create the CA at `stateDir/mtls/ca.{crt,key}` |
| `IssueServerCert(ca, caKey, hostnames)` | Issue a server cert for the given hostnames/IPs |
| `IssueClientCert(ca, caKey, name)` | Issue a client cert with CN=name |
| `PersistClientCert(stateDir, name, cert)` | Write client cert to `stateDir/mtls/clients/<name>.{crt,key}` |
| `LoadClientCert(stateDir, name)` | Load a previously persisted client cert |
| `BuildServerTLSConfig(serverCert, caPool)` | Build `tls.Config` with `RequireAndVerifyClientCert` |
| `BuildClientTLSConfig(clientCert, caPool)` | Build `tls.Config` for client-side mTLS |
| `CertPoolFromCert(cert)` | Create a cert pool from a single certificate |
| `IsNonLoopback(addr)` | True when addr is not 127.x, ::1, or localhost |

## Non-loopback enforcement

`IsNonLoopback` is the gate for mTLS enforcement. The daemon **must** verify
this before binding any non-loopback address:

```go
if mtls.IsNonLoopback(addr) {
    // mTLS is required; fail if CA/cert not available
}
```

Loopback listeners (the default) continue to use bearer tokens. This is
fail-closed: attempting to bind a non-loopback address without calling
`LoadOrGenerateCA` and `IssueServerCert` first is a programming error, not
a graceful degradation.

## TLS 1.3 alert timing

In TLS 1.3, the server sends a post-handshake `certificate_unknown` alert
when rejecting an untrusted client certificate. The `tls.Dial` call may
return before this alert arrives. Callers that need to verify rejection must
attempt a `Read` on the connection after `Dial` to surface the error. The
`TestMutualTLS_UntrustedClient_Rejected` test demonstrates this pattern.

## Platform notes

Files are created at mode 0600 on Unix. Windows ACL hardening is a
Phase 1.5 follow-up (#3); on Windows the files use the default process ACL
and the operator must restrict manually.

## Usage example

```go
// Server setup
caCert, caKey, err := mtls.LoadOrGenerateCA(stateDir)
serverCert, err := mtls.IssueServerCert(caCert, caKey, []string{"192.168.1.10"})
caPool := mtls.CertPoolFromCert(caCert)
tlsCfg := mtls.BuildServerTLSConfig(serverCert, caPool)
ln, err := tls.Listen("tcp", "192.168.1.10:7893", tlsCfg)

// Client setup
clientCert, err := mtls.LoadClientCert(stateDir, "alice")
tlsCfg := mtls.BuildClientTLSConfig(clientCert, caPool)
conn, err := tls.Dial("tcp", "192.168.1.10:7893", tlsCfg)
```

## Testing

```bash
go test ./internal/mtls/
```

20+ tests covering CA creation and persistence, server/client cert issuance,
mutual TLS roundtrip, untrusted client rejection, and `IsNonLoopback`
variants.
