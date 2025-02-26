package log

import (
	"context"
	"fmt"
	"log"
	"strings"
)

type stdLogger struct {
	l *log.Logger
}

func (l *stdLogger) Infof(ctx context.Context, format string, args ...any) {
	outputLogf(ctx, l.l, "INFO", format, args...)
}

func (l *stdLogger) Warnf(ctx context.Context, format string, args ...any) {
	outputLogf(ctx, l.l, "WARN", format, args...)
}

func (l *stdLogger) Errorf(ctx context.Context, format string, args ...any) {
	outputLogf(ctx, l.l, "ERROR", format, args...)
}

func (l *stdLogger) Debugf(ctx context.Context, format string, args ...any) {
	outputLogf(ctx, l.l, "DEBUG", format, args...)
}

func outputLogf(ctx context.Context, l *log.Logger, prefix, format string, args ...any) {
	b := strings.Builder{}
	tID := TrackTransportID(ctx)
	if tID != "" {
		b.WriteString("track-transport-id:" + tID + "\t")
	}
	mID := TrackMessageID(ctx)
	if mID != "" {
		b.WriteString("track-message-id:" + mID + "\t")
	}
	b.WriteString(format)
	l.Output(3, fmt.Sprintf("%s: %s", prefix, fmt.Sprintf(b.String(), args...)))
}

// NewStdは、`log` パッケージのロガーを返却します。
func NewStd() Logger {
	return &stdLogger{
		l: log.Default(),
	}
}

// NewStdは、`log` パッケージのロガーを返却します。
func NewStdWith(l *log.Logger) Logger {
	return &stdLogger{
		l: l,
	}
}
