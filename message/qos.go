package message

import "fmt"

// QoS（Quality of Service）は、トランスポートのコネクションが切断された場合のデータの取扱を規定します。
type QoS uint8

const (
	QoSUnreliable QoS = iota // 低信頼
	QoSReliable              // 高信頼
	QoSPartial               // 信頼性のあるトランスポートを利用する低信頼
)

func (q QoS) String() string {
	switch q {
	case QoSUnreliable:
		return "unreliable"
	case QoSReliable:
		return "reliable"
	case QoSPartial:
		return "partial"
	default:
		return fmt.Sprint(uint8(q))
	}
}
