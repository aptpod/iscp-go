package convert

// nilOrEmpty converts a non-nil pointer to an empty target struct.
// Used for extension fields that carry no data.
func nilOrEmpty[In, Out any](in *In) *Out {
	if in == nil {
		return nil
	}
	return new(Out)
}
