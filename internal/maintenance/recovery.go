package maintenance

import (
	"context"
	"errors"
	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
)

type recoverySource interface {
	Recovery(context.Context) (aimili.Recovery, error)
	UpdateRecovery(context.Context, aimili.RecoverySettings) (aimili.Recovery, error)
	RetryDedicatedStandby(context.Context, int) error
}

func recoveryError(err error) error {
	if err == nil {
		return nil
	}
	var adapterError *aimili.AdapterError
	if errors.As(err, &adapterError) {
		switch adapterError.Code {
		case "invalid_request", "invalid_recovery_settings", "recovery_settings_storage_failed", "operation_busy":
			return &Error{Code: adapterError.Code}
		}
	}
	return &Error{Code: "service_unavailable"}
}

func (s *Service) Recovery(ctx context.Context) (aimili.Recovery, error) {
	source, ok := s.aimili.(recoverySource)
	if !ok {
		return aimili.Recovery{}, &Error{Code: "not_configured"}
	}
	result, err := source.Recovery(ctx)
	return result, recoveryError(err)
}

func (s *Service) UpdateRecovery(ctx context.Context, settings aimili.RecoverySettings) (aimili.Recovery, error) {
	source, ok := s.aimili.(recoverySource)
	if !ok {
		return aimili.Recovery{}, &Error{Code: "not_configured"}
	}
	result, err := source.UpdateRecovery(ctx, settings)
	return result, recoveryError(err)
}

func (s *Service) RetryDedicatedStandby(ctx context.Context, index int) error {
	source, ok := s.aimili.(recoverySource)
	if !ok {
		return &Error{Code: "not_configured"}
	}
	return recoveryError(source.RetryDedicatedStandby(ctx, index))
}
