package iscp

// SendMetadataOptionは、メタデータ送信時のオプションです。
type SendMetadataOption func(opts *sendMetadataOptions)

var defaultSendMetadataOptions = sendMetadataOptions{
	Persist: false,
}

// sendMetadataOptions are the options for sending metadata.
type sendMetadataOptions struct {
	Persist bool // Whether to persist
}

// WithSendMetadataPersistは、メタデータ送信時にメタデータを永続化します。
func WithSendMetadataPersist() SendMetadataOption {
	return func(opts *sendMetadataOptions) {
		opts.Persist = true
	}
}
