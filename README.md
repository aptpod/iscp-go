# iSCP-go

iSCPv2 Client Library

## Installation

- Execute go get command

  ```sh
  go get github.com/aptpod/iscp-go
  ```

## Usage

- See [Example](./examples)

## WebSocket

The default WebSocket implementation uses [coder/websocket](https://github.com/coder/websocket).
No build tags or blank imports are required.

To use [gorilla/websocket](https://github.com/gorilla/websocket) instead, specify `GorillaDial` in `DialerConfig`:

```go
d := websocket.NewDialer(websocket.DialerConfig{
    DialFunc: websocket.GorillaDial,
})
```

## Development

1. Fork this repository
1. Clone this repository
1. Change the origin url of the cloned repository as below.

   ```sh
   git remote set-url origin <your forked repository>
   ```

## References

- [GoDoc](https://pkg.go.dev/github.com/aptpod/iscp-go)
- [GitHub](https://github.com/aptpod/iscp-go/)
