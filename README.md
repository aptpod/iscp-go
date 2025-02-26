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

The implementation of WebSocket is as follows.
You can switch implementations using build tags.

- [coder/websocket](https://github.com/coder/websocket) (Default)
- [gorilla/websocket](https://github.com/gorilla/websocket) (`gorilla`)
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
