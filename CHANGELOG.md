# CHANGELOG.md

## v1.2.0

- Add resume token support for stream resumption.

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
