package domain

import (
	"errors"
	"time"
)

type ProtocolMode string

const (
	ProtocolVLESSTCPRealityVision ProtocolMode = "vless_tcp_reality_vision"
	ProtocolVLESSXHTTPReality     ProtocolMode = "vless_xhttp_reality"
	ProtocolHysteria2QUICTLS      ProtocolMode = "hysteria2_quic_tls"
)

func (mode ProtocolMode) Valid() bool {
	switch mode {
	case ProtocolVLESSTCPRealityVision, ProtocolVLESSXHTTPReality, ProtocolHysteria2QUICTLS:
		return true
	default:
		return false
	}
}

type ProtocolState string

const (
	ProtocolReady               ProtocolState = "ready"
	ProtocolSwitching           ProtocolState = "switching"
	ProtocolSubscriptionPending ProtocolState = "subscription_pending"
	ProtocolRollingBack         ProtocolState = "rolling_back"
	ProtocolRepairRequired      ProtocolState = "repair_required"
)

func (state ProtocolState) Valid() bool {
	switch state {
	case ProtocolReady, ProtocolSwitching, ProtocolSubscriptionPending, ProtocolRollingBack, ProtocolRepairRequired:
		return true
	default:
		return false
	}
}

type EgressProtocolMode struct {
	EgressID        string
	ActiveMode      ProtocolMode
	DesiredMode     ProtocolMode
	State           ProtocolState
	LastOperationID string
	LastRequestHash string
	LastErrorCode   string
	Version         int64
	UpdatedAt       time.Time
}

func (record *EgressProtocolMode) Transition(next ProtocolState) error {
	if record == nil || !next.Valid() {
		return errors.New("invalid protocol transition")
	}
	allowed := map[ProtocolState]map[ProtocolState]bool{
		ProtocolReady: {
			ProtocolSwitching:      true,
			ProtocolRepairRequired: true,
		},
		ProtocolSwitching: {
			ProtocolSubscriptionPending: true,
			ProtocolRollingBack:         true,
			ProtocolRepairRequired:      true,
		},
		ProtocolSubscriptionPending: {
			ProtocolReady:          true,
			ProtocolRollingBack:    true,
			ProtocolRepairRequired: true,
		},
		ProtocolRollingBack: {
			ProtocolReady:          true,
			ProtocolRepairRequired: true,
		},
		ProtocolRepairRequired: {
			ProtocolReady: true,
		},
	}
	if !allowed[record.State][next] {
		return errors.New("invalid protocol transition")
	}
	record.State = next
	return nil
}
