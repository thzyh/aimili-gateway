package adapters

import (
	"context"
	"time"
)

type Health string

const (
	HealthHealthy     Health = "healthy"
	HealthDegraded    Health = "degraded"
	HealthUnavailable Health = "unavailable"
)

type ProbeResult struct {
	Service      string    `json:"service"`
	Health       Health    `json:"health"`
	Version      string    `json:"version,omitempty"`
	Capabilities []string  `json:"capabilities"`
	ErrorCode    string    `json:"errorCode,omitempty"`
	CheckedAt    time.Time `json:"checkedAt"`
}

type Prober interface {
	Probe(context.Context) ProbeResult
}
