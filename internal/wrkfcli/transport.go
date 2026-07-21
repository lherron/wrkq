package wrkfcli

import (
	"encoding/json"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	workrpcclient "github.com/lherron/wrkq/internal/workrpc/client"
	"github.com/spf13/cobra"
)

func withTransport(fn func(*app, *cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if err := resolveStdinTextFlags(cmd); err != nil {
			return err
		}
		actor, wrkqActor, err := actorDefaults(cmd)
		if err != nil {
			return err
		}
		tr, _, closeTransport, err := openConfiguredTransport(cmd)
		if err != nil {
			return err
		}
		defer closeTransport()
		a := &app{
			actor: actor, wrkqActor: wrkqActor, role: roleDefault(), json: flagJSON,
			transport: tr,
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

// openConfiguredTransport is the single local-vs-remote selection point for
// wrkf command adapters. Commands added to the transport path must not branch
// on the locator themselves.
func openConfiguredTransport(cmd *cobra.Command) (workrpcclient.Transport, *config.Config, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	if flagDB != "" {
		if err := config.ApplyDBLocator(cfg, flagDB, false); err != nil {
			return nil, nil, nil, err
		}
	}
	if cfg.RemoteEndpoint != "" {
		tr, err := workrpcclient.NewRemote(cfg.RemoteEndpoint, workrpcclient.TokenFromEnv(), workrpcclient.WrkfProfile)
		if err != nil {
			return nil, nil, nil, err
		}
		return tr, cfg, func() { _ = tr.Close() }, nil
	}
	h, err := bootstrap.Open(cfg.DBLocator)
	if err != nil {
		return nil, nil, nil, err
	}
	h.Opts.Entrypoint = "wrkf"
	tr, err := workrpcclient.NewInProcess(h, workrpcclient.WrkfProfile)
	if err != nil {
		_ = h.Close()
		return nil, nil, nil, err
	}
	return tr, h.Config, func() { _ = tr.Close() }, nil
}
