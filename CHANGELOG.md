# CHANGELOG.md

## Unreleased

- Update `quic-go` to v0.66.0 and `webtransport-go` to v0.13.0. This raises the minimum Go version to 1.26.
- Replace gogo/protobuf with the generated code in `github.com/aptpod/iscp-proto/gen/go` (google.golang.org/protobuf and vtprotobuf).
  - `encoding/protobuf` encodes and decodes with vtprotobuf. The encoded bytes are unchanged.
  - `encoding/json` uses protojson. Whitespace and key order are no longer stable, nil bytes are encoded as `""`, and HTML characters are not escaped. Empty `downstream_filter_references` and empty bytes are decoded as nil, the same as with protobuf. Invalid UTF-8, duplicate keys and trailing data are rejected.
  - Breaking: the protobuf types used by `encoding/convert` change from `iscp-proto/gen/gogofast/iscp2/v1` to `iscp-proto/gen/go/iscp2/v1`.
- Remove dependencies
  - github.com/gogo/protobuf

## v1.2.0

- Add resume token support for stream resumption.
- Support Multi Connection.
  - Add multi-connection scheduler support with three algorithms: ECF (Earliest Completion First), MinRTT (Minimum Round-Trip Time), and Round-Robin

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
