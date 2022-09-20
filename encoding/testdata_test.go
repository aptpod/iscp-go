package encoding_test

import "github.com/aptpod/iscp-go/message"

/*
Ping messages
*/

func getPing() message.Message {
	return &message.Ping{
		RequestID:       1,
		ExtensionFields: &message.PingExtensionFields{},
	}
}

func getPong() *message.Pong {
	return &message.Pong{
		RequestID:       1,
		ExtensionFields: &message.PongExtensionFields{},
	}
}
