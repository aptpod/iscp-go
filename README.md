# iSCP-go

iSCPv2 Client Library

## Installation

- Execute go get command

    ```sh
    go get github.com/aptpod/iscp-go
    ```

- Replace your go.mod file as below

    ```go.mod
    replace github.com/quic-go/quic-go => github.com/aptpod/quic-go aptpod-webtransport
    replace github.com/quic-go/webtransport-go => github.com/aptpod/webtransport-go aptpod-webtransport
    ```

- Execute `go mod tidy`

    ```sh
    go mod tidy
    ```

## Usage

- See [Example](./examples)

## WebSocket

The implementation of WebSocket is as follows.
You can switch implementations using build tags.

- [gorilla/websocket](https://github.com/gorilla/websocket) (Default)
- [nhooyr/websocket](https://github.com/nhooyr/websocket) (`nhooyr`)

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
