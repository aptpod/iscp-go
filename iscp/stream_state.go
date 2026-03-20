package iscp

type streamStatus uint8

const (
	_ streamStatus = iota
	streamStatusConnected
	streamStatusResuming
	streamStatusDraining
)

type streamState struct {
	*stateMachine[streamStatus]
}

func newStreamState() *streamState {
	return &streamState{
		stateMachine: newStateMachine(streamStatusConnected),
	}
}
