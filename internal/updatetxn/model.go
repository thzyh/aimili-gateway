package updatetxn

import (
	"errors"
	"time"
)

var (
	ErrInvalidRequest  = errors.New("invalid update request")
	ErrRunConflict     = errors.New("update run conflicts with an existing request")
	ErrUpdateBusy      = errors.New("another update is active")
	ErrUntrustedResult = errors.New("update result metadata is untrusted")
)

type Kind string

const (
	KindUI      Kind = "ui"
	KindGateway Kind = "gateway"
)

type Action string

const (
	ActionApply    Action = "apply"
	ActionRollback Action = "rollback"
)

type State string

const (
	StatePending        State = "pending"
	StateDownloading    State = "downloading"
	StateValidating     State = "validating"
	StateSwitching      State = "switching"
	StateVerifying      State = "verifying"
	StateRolledBack     State = "rolled_back"
	StateSuccess        State = "success"
	StateFailed         State = "failed"
	StateRepairRequired State = "repair_required"
)

type Request struct {
	RunID       string    `json:"runId"`
	Kind        Kind      `json:"kind"`
	Version     string    `json:"version,omitempty"`
	Action      Action    `json:"action"`
	DryRun      bool      `json:"dryRun"`
	RequestedAt time.Time `json:"requestedAt"`
}

type Result struct {
	RunID      string     `json:"runId"`
	Kind       Kind       `json:"kind"`
	Version    string     `json:"version,omitempty"`
	State      State      `json:"state"`
	ErrorCode  string     `json:"errorCode,omitempty"`
	StartedAt  time.Time  `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

func (s State) Terminal() bool {
	switch s {
	case StateRolledBack, StateSuccess, StateFailed, StateRepairRequired:
		return true
	default:
		return false
	}
}
