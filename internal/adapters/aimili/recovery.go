package aimili

import (
	"context"
	"fmt"
	"net/http"
)

type RecoverySettings struct {
	FailureThreshold         int  `json:"failureThreshold"`
	HealthIntervalSeconds    int  `json:"healthIntervalSeconds"`
	StandbyIntervalSeconds   int  `json:"standbyIntervalSeconds"`
	StandbyFailureThreshold  int  `json:"standbyFailureThreshold"`
	DialTimeoutSeconds       int  `json:"dialTimeoutSeconds"`
	CandidatesPerRound       int  `json:"candidatesPerRound"`
	MaxConcurrentDials       int  `json:"maxConcurrentDials"`
	RetryInitialSeconds      int  `json:"retryInitialSeconds"`
	RetryMaxSeconds          int  `json:"retryMaxSeconds"`
	CandidateCooldownSeconds int  `json:"candidateCooldownSeconds"`
	RecoveryBudgetSeconds    int  `json:"recoveryBudgetSeconds"`
	FreshnessSeconds         int  `json:"freshnessSeconds"`
	AllowCrossCountry        bool `json:"allowCrossCountry"`
	AllowDatacenter          bool `json:"allowDatacenter"`
}

type Recovery struct {
	Settings            RecoverySettings         `json:"settings"`
	Bounds              map[string][]int         `json:"bounds"`
	ActiveTargets       []DedicatedStandbyConfig `json:"activeTargets"`
	ActiveTargetCount   int                      `json:"activeTargetCount"`
	StandbyTargetCount  int                      `json:"standbyTargetCount"`
	RegularExitSlotsMax int                      `json:"regularExitSlotsMax"`
	AutoManaged         bool                     `json:"autoManaged"`
	IPQualityStatus     string                   `json:"ipQualityStatus"`
}

func (c *Client) Recovery(ctx context.Context) (Recovery, error) {
	var result Recovery
	err := c.do(ctx, c.readTimeout, http.MethodGet, "control/v1/recovery", nil, &result)
	return result, err
}

func (c *Client) UpdateRecovery(ctx context.Context, settings RecoverySettings) (Recovery, error) {
	var result Recovery
	input := struct {
		Settings RecoverySettings `json:"settings"`
	}{settings}
	err := c.do(ctx, c.operationTimeout, http.MethodPut, "control/v1/recovery", input, &result)
	return result, err
}

func (c *Client) RetryDedicatedStandby(ctx context.Context, index int) error {
	var result struct{}
	return c.do(ctx, c.readTimeout, http.MethodPost, fmt.Sprintf("control/v1/standbys/%d/retry", index), struct{}{}, &result)
}
