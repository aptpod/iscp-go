//go:build !stress

package iscp_test

// stressGoroutines is the number of goroutines to run concurrently.
const stressGoroutines = 4

// stressIterationsSlow is the iteration count for wall-clock dependent tests only,
// where each iteration includes a fixed time.Sleep. TestConn_Close_TimesOutWhenDisconnectSendBlocks_Repeated waits
// for disconnectSendTimeout (3s) every time, so using stressIterations as is
// would simply inflate the wait time.
const stressIterationsSlow = 2
