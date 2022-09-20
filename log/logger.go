package log

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Loggerは、iscp-go内で使用するロガーインターフェースです。
type Logger interface {
	Infof(context.Context, string, ...interface{})
	Warnf(context.Context, string, ...interface{})
	Errorf(context.Context, string, ...interface{})
	Debugf(context.Context, string, ...interface{})
}

var (
	trackTransportIDKey        = "trackTransportIDKey"
	trackMessageTransportIDKey = "trackMessageIDKey"
)

// WithTrackTransportIDは、新たにトランスポートIDを採番しコンテキストにセットします。
//
// トランスポートIDはトランスポートが開通されたタイミングでセットします。
// ここで設定されたトランスポートIDは常にログ出力します。
func WithTrackTransportID(ctx context.Context) context.Context {
	return context.WithValue(ctx, &trackTransportIDKey, genTrackID())
}

// TrackTransportIDは、コンテキストにセットされたトランスポートIDを取得します。
func TrackTransportID(ctx context.Context) string {
	v, ok := ctx.Value(&trackTransportIDKey).(string)
	if !ok {
		return ""
	}
	return v
}

// WithTrackMessageIDは、新たにメッセージIDを採番しコンテキストにセットします。
//
// メッセージIDはメッセージ受信したタイミングでセットします。
// ここで設定されたメッセージIDは常にログ出力します。
func WithTrackMessageID(ctx context.Context) context.Context {
	return context.WithValue(ctx, &trackMessageTransportIDKey, genTrackID())
}

// TrackMessageIDは、コンテキストにセットされたメッセージIDを取得します。
func TrackMessageID(ctx context.Context) string {
	v, ok := ctx.Value(&trackMessageTransportIDKey).(string)
	if !ok {
		return ""
	}
	return v
}

func genTrackID() string {
	return fmt.Sprintf("%04d-%04d-%04d", rand.Int31n(10000), rand.Int31n(10000), rand.Int31n(10000))
}
