# Local quic-go patch

This directory contains the source of `github.com/quic-go/quic-go` v0.62.0,
the version pinned by the Gateway before this patch. The upstream source and
license are retained. Gateway maintains one focused extension: `Config` exposes
`MaxPacketSize`, and QUIC path-MTU discovery uses that value as an upper bound
on every active and migrated path. This is required to implement the documented
`listener.limits.quic.maxPacketBytes` contract; `InitialPacketSize` is not an
equivalent setting because it configures the starting/lower size.

The module replacement in the Gateway `go.mod` keeps this transport change
local and reproducible for macOS and Linux builds. When upstream provides an
equivalent supported API, this patch should be rebased or removed after the
HTTP/3 packet-limit conformance test passes against it.
