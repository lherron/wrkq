//go:build wrkq_local

package client

import "github.com/lherron/wrkq/internal/config"

func newConfiguredLocalTransport(cfg *config.Config, principalRef string) (Transport, error) {
	return NewConfiguredInProcess(cfg.DBLocator, "", LocalServerOptions{
		Entrypoint:              "go-client",
		DefaultPrincipalRef:     principalRef,
		WrkqDefaultPrincipalRef: principalRef,
		UseWrkqDefault:          true,
	}, WrkqProfile)
}
