package wrkfcli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/config"
	workrpcclient "github.com/lherron/wrkq/internal/workrpc/client"
	"github.com/spf13/cobra"
)

func withTransport(fn func(*app, *cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if err := resolveStdinTextFlags(cmd); err != nil {
			return err
		}
		cfg, err := loadConfiguredConfig()
		if err != nil {
			return err
		}
		principalRef, err := wrkfPrincipalDefault(cmd, cfg)
		if err != nil {
			return err
		}
		tr, closeTransport, err := openConfiguredTransportWithConfig(cmd, cfg, principalRef)
		if err != nil {
			return err
		}
		defer closeTransport()
		a := &app{
			principalRef: principalRef,
			role:         roleDefault(),
			json:         flagJSON,
			transport:    tr,
			remote:       cfg.RemoteEndpoint != "",
		}
		runErr := fn(a, cmd, args)
		if runErr != nil && flagJSON {
			if errorAlreadyReported(runErr) {
				return runErr
			}
			renderJSONErrorEnvelope(cmd, runErr)
			if exit, ok := runErr.(cliExitError); ok {
				return exitErrorReported(exit.code, runErr)
			}
			return errReported
		}
		return runErr
	}
}

func rpcCall[T any](cmd *cobra.Command, a *app, method string, params any) (T, error) {
	var out T
	raw, err := a.transport.Call(cmd.Context(), method, params)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}

func rawJSON(value string) json.RawMessage {
	if value == "" {
		return nil
	}
	return json.RawMessage(value)
}

func rawJSONString(value string) json.RawMessage {
	if value == "" {
		return nil
	}
	raw, _ := json.Marshal(value)
	return raw
}

// openConfiguredTransport is the single local-vs-remote selection point for
// wrkf command adapters. Commands added to the transport path must not branch
// on the locator themselves.
func openConfiguredTransport(cmd *cobra.Command) (workrpcclient.Transport, *config.Config, func(), error) {
	cfg, err := loadConfiguredConfig()
	if err != nil {
		return nil, nil, nil, err
	}
	tr, closeTransport, err := openConfiguredTransportWithConfig(cmd, cfg, "")
	return tr, cfg, closeTransport, err
}

func loadConfiguredConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if flagDB != "" {
		if err := config.ApplyDBLocator(cfg, flagDB, false); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func openConfiguredTransportWithConfig(cmd *cobra.Command, cfg *config.Config, principalRef string) (workrpcclient.Transport, func(), error) {
	if cfg.RemoteEndpoint != "" {
		if flagHookCatalog != "" {
			return nil, nil, fmt.Errorf("--hook-catalog is local-only; hook catalog is canonical-node configuration in remote mode")
		}
		tr, err := workrpcclient.NewRemote(cfg.RemoteEndpoint, workrpcclient.TokenFromEnv(), workrpcclient.WrkfProfile)
		if err != nil {
			return nil, nil, err
		}
		return tr, func() { _ = tr.Close() }, nil
	}
	tr, err := workrpcclient.NewConfiguredInProcess(
		cfg.DBLocator,
		flagHookCatalog,
		workrpcclient.LocalServerOptions{
			Entrypoint:              "wrkf",
			DefaultPrincipalRef:     principalRef,
			WrkqDefaultPrincipalRef: principalRef,
			UseWrkqDefault:          true,
			DefaultRole:             roleDefault(),
		},
		workrpcclient.WrkfProfile,
	)
	if err != nil {
		return nil, nil, err
	}
	return tr, func() { _ = tr.Close() }, nil
}
