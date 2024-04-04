package websocket

func CallDialFunc(url string, tk *Token) (Conn, error) {
	return dialFunc(DialConfig{
		URL:                url,
		Token:              tk,
		EnableMultipathTCP: true,
	})
}
