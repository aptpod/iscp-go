package log

import "context"

type nopLogger struct{}

func (l *nopLogger) Infof(ctx context.Context, _ string, _ ...any)  {}
func (l *nopLogger) Warnf(ctx context.Context, _ string, _ ...any)  {}
func (l *nopLogger) Errorf(ctx context.Context, _ string, _ ...any) {}
func (l *nopLogger) Debugf(ctx context.Context, _ string, _ ...any) {}

// NewNopは、何も出力しないロガーを返却します。
func NewNop() Logger {
	return &nopLogger{}
}
