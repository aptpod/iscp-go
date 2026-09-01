# CHANGELOG.md

## v1.2.1

- Fix a permanent transport leak when `Close` races with the initial dial in the reconnect transport. The dialed transport was assigned after `Close` had already finished, leaving nobody to close it.
- Fix a QUIC session and UDP socket leak when negotiation or transport construction fails during dialing.
- Fix a goroutine leak when `Subscribe` is called on a closed NIC manager. The returned channel was never closed, so the subscriber goroutine blocked forever.
- Fix `Upstream.resume` and `Downstream.resume` retrying without regard to the stream context. Cancellation now interrupts the backoff sleep instead of waiting for it to elapse (up to 5 seconds after `Close`).
- Fix the reconnect transport reporting `StatusReconnecting` after it was closed, overwriting the `StatusDisconnected` set by `Close`.
- Fix `ChangeNIC` returning success on a closed NIC manager. The send and the cancellation were both ready in a `select`, so the outcome was random.
- Fix the protobuf codec collapsing a typed error recovered from a panic into a plain string error, which broke `errors.Is` and `errors.As`.
- Fix `TOO_SHORT_PING_INTERVAL` missing from the result code conversion tables, which made the whole message fail to decode when a server returned it.
- **Behavior change**: `nic.NewDialContext` no longer resolves the interface address at construction time. The address is resolved on each dial, so a NIC that is absent or has a different address at startup no longer breaks the client permanently. `NewDialContext` now succeeds for an unknown interface and the error surfaces from `DialContext` instead. Link-local addresses (169.254.0.0/16) are also excluded from selection.

## v1.2.0

- Add resume token support for stream resumption.
- Support Multi Connection.
  - Add multi-connection scheduler support with three algorithms: ECF (Earliest Completion First), MinRTT (Minimum Round-Trip Time), and Round-Robin
- **Behavior change**: `Downstream.Close` now waits at most 10 seconds (by default) for the final Ack flush before returning, instead of blocking indefinitely as before. Configure this via the new `WithDownstreamCloseTimeout` option.
- **Behavior change**: when `Upstream.Close` runs concurrently with an internal tear-down (a failed resume or a failed flush validation), only one path now sends the CloseRequest and fires the Closed event; the other returns nil without waiting for the tear-down to finish. Previously both paths ran, sending the CloseRequest twice, firing the Closed event twice, and letting the loser cancel the winner's in-flight request. Calling `Close` twice still returns `already draining` for the second call.
- Fix several crashes (process panics) caused by concurrent map access in the wire layer, a race in the NIC manager, a panic after a reconnect dial succeeds, and a torn read of the transport metrics provider.
- Fix several cases of indefinite blocking: the Connect handshake, `Conn.Close`, `Conn.send`, `Downstream.Close`, upstream stalls caused by delayed Acks, and `Conn.Close` racing with an in-progress reconnect.
- Fix data loss where unacknowledged chunks could be dropped from the Reliable QoS resend set, and where Acks were ignored after a reconnect generation change.
- Fix WebSocket transport reporting a failed send as successful.
- Fix data races in `Upstream`'s and `Downstream`'s connection reference, `Downstream`'s final-ack notification channel, the wire layer's Ack map, and the reconnect transport's internal state.
- Fix `AckTimeout` documentation, which incorrectly described the connection being disconnected on timeout.
- `Upstream.Flush` now also respects `closeTimeout` when called as part of `Close`.
- Update `quic-go` to v0.62.0 and `webtransport-go` to v0.13.0. This raises the minimum Go version to 1.26.

## v1.1.0

- Fix compression bugs.
- Improve connect request error handling
- Ensure compression is disabled when no compression parameters are provided
- Remove dependencies
  - github.com/aptpod/quic-go
  - github.com/aptpod/webtransport-go
- Improve error handling in SendMetadata method to handle different response scenarios

## v1.0.0

- GA

## v0.12.0

- Support MultipathTCP mode in websocket.
- Support Reliable QoS

## v0.11.0

- Support `omit_empty_chunk`.
- Add `tls.Config` to WebSocket configurations.

## v0.10.0

- Change the priority type of basetime from "uint32" to "uint8"

## v0.9.0

- New iSCP Client!
