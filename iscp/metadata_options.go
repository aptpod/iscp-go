package iscp

// SendMetadataOptionは、メタデータ送信時のオプションです。
type SendMetadataOption func(opts *sendMetadataOptions)

var defaultSendMetadataOptions = sendMetadataOptions{
	Persist: false,
}

// sendMetadataOptionsは、メタデータ送信時のオプションです。
type sendMetadataOptions struct {
	Persist bool // 永続化するかどうか
}

// WithSendMetadataPersistは、メタデータ送信時にメタデータを永続化します。
func WithSendMetadataPersist() SendMetadataOption {
	return func(opts *sendMetadataOptions) {
		opts.Persist = true
	}
}
