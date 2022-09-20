package websocket

func CallDialFunc(url string, tk *Token) (Conn, error) {
	return dialFunc(url, tk)
}
