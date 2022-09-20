package iscp

// Version は このライブラリのバージョンです。
const Version = "v0.44.0"

func SemVersion() string {
	return Version[1:]
}

// ProtocolVersion は プロトコルのバージョンです。
const ProtocolVersion = "2.0.0"
