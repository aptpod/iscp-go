package wire

import (
	"fmt"
)

// Sizeは、バイトサイズの単位です。
type Size int64

const (
	B  Size = 1
	KB      = B * 1000
	MB      = KB * 1000
	GB      = MB * 1000
	TB      = GB * 1000
	PB      = TB * 1000
)

func (s Size) String() string {
	if s == 0 {
		return "0[b]"
	}
	format := func(val Size, unit Size, unitString string) string {
		return fmt.Sprintf("%.2f[%s]", float64(val)/float64(unit), unitString)
	}
	if s >= PB {
		return format(s, PB, "pb")
	}
	if s >= TB {
		return format(s, TB, "tb")
	}
	if s >= GB {
		return format(s, GB, "gb")
	}
	if s >= MB {
		return format(s, MB, "mb")
	}
	if s >= KB {
		return format(s, KB, "kb")
	}
	return format(s, B, "b")
}
