package iscp

// Version は このライブラリのバージョンです。
const Version = "v1.1.0-next"

func SemVersion() string {
	return Version[1:]
}

// ProtocolVersion は プロトコルのバージョンです。
const ProtocolVersion = "2.1.0"
