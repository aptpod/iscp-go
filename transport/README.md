# トランスポート層
トランスポート層では、エンコード層によりシリアライズされたバイナリの **下位のトランスポートプロトコルへの格納方法** を定義します。

## トランスポート層のインターフェース
トランスポート層が実装すべきインターフェースについては、ワイヤ層の [Transport インターフェース](../wire/transport.go) をご参照ください。

## 対応トランスポート一覧

- [WebSocket](./websocket)
- [QUIC](./quic)
- [General Stream-Oriented Transport](./netconn)
  - TCP
  - TLS
  - DTLS
  - Unix Domain Socket
  - etc ...
