package pkg

import (
	"context"
	"errors"
)

var (
	ErrInvalidAuth      = errors.New("invalid auth")
	ErrUnauthorized     = errors.New("unauthorized")
	ErrDataRanOut       = errors.New("data ran out")
	ErrBlockedDomain    = errors.New("domain blocked")
	ErrBlockedIP        = errors.New("blocked ip address")
	ErrInvalidParam     = errors.New("invalid param")
	ErrInvalidTargeting = errors.New("invalid targeting")
	ErrUnknownCountry   = errors.New("unknown country")
	ErrUnknownRegion    = errors.New("unknown region")
	ErrUnknownCity      = errors.New("unknown city")
)

type Permission uint

const (
	PermissionUDP Permission = iota + 1
)

type Auth interface {
	Authenticate(ctx context.Context, apiKey []byte) (userId string, permissions []Permission, err error)
}
