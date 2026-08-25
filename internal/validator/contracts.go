package validator

import (
	"errors"
	"time"
)

type Result struct {
	ExitIP      string
	DNSVerified bool
	Latency     time.Duration
}

type Error struct {
	Code string
}

func (e *Error) Error() string { return "proxy validation failed: " + e.Code }

type Validator struct {
	timeout time.Duration
}

func New(timeout time.Duration) *Validator {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Validator{timeout: timeout}
}

func validationFailure(code string) error {
	if code == "" {
		return errors.New("validation failure code is required")
	}
	return &Error{Code: code}
}
