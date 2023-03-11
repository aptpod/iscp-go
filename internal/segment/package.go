// Package segment provides functions to segment the binary of ISCP messages in Transport layer.
package segment

import "time"

var (
	maxDatagramFrameSize = 1196
	maxPayloadSize       = maxDatagramFrameSize - 8
	timeNow              = time.Now
)
