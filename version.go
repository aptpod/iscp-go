package iscp

// Version は このライブラリのバージョンです。
const Version = "v1.0.0"

func SemVersion() string {
	return Version[1:]
}

// ProtocolVersion は プロトコルのバージョンです。
const ProtocolVersion = "2.0.0"
