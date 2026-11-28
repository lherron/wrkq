package rpccli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/workrpc"
	workrpcclient "github.com/lherron/wrkq/pkg/client"
	"github.com/spf13/cobra"
)

type clientUpdater struct {
	executable func() (string, error)
	reexec     func(string, []string, []string) error
}

var revisionPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var commitPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

type clientVersion struct {
	Commit             string `json:"commit"`
	ProtocolVersion    string `json:"protocol_version"`
	ProtocolSchemaHash string `json:"protocol_schema_hash"`
}

func (u clientUpdater) preRun(cmd *cobra.Command, _ []string) error {
	top := cmd
	for top.HasParent() && top.Parent().HasParent() {
		top = top.Parent()
	}
	switch top.Name() {
	case "version", "help", "completion", "config", "self-update", "rpc", "agent", "server",
		"__complete", "__completeNoDesc", "usage", "info", "agent-info":
		return nil
	}
	if _, updated := os.LookupEnv("WRKQ_SELF_UPDATED"); updated || os.Getenv("WRKQ_CLIENT_SELF_UPDATE") == "0" {
		return nil
	}
	path, err := u.installedPath()
	if err != nil {
		return nil
	}
	cfg, getenv, err := config.PreviewWithDBOverride(dbOverride(cmd))
	if err != nil || !selfUpdateEndpointMatches(cfg) {
		return nil
	}
	token, _ := workrpc.ResolveTokenFromEnv(getenv)
	ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
	tr, _, err := workrpcclient.NewRemoteWithIdentity(ctx, cfg.RemoteEndpoint, token, workrpcclient.WrkqProfile)
	cancel()
	if err == nil {
		cmd.SetContext(context.WithValue(cmd.Context(), preflightKey{}, preflightRemote{
			Transport: tr, endpoint: cfg.RemoteEndpoint, token: token,
		}))
		return nil
	}
	var mismatch *workrpcclient.ProtocolMismatchError
	if !errors.As(err, &mismatch) || !revisionPattern.MatchString(mismatch.ServerRevision) {
		return nil
	}
	target := mismatchIdentity(mismatch)
	_, updateErr := updateInstalledClient(cmd, path, cfg.RemoteEndpoint, token, target, false)
	if updateErr == nil {
		updateErr = u.reexec(path, os.Args, append(os.Environ(), "WRKQ_SELF_UPDATED="+target.Revision))
	}
	if updateErr != nil {
		printUpdateFailure(cmd, updateErr)
	}
	return nil
}

func selfUpdateEndpointMatches(cfg *config.Config) bool {
	optIn := strings.TrimSpace(cfg.ClientSelfUpdateEndpoint)
	if cfg.RemoteEndpoint == "" || optIn == "" {
		return false
	}
	var release config.Config
	if err := config.ApplyDBLocator(&release, "rpc://"+optIn, false); err != nil {
		return false
	}
	// The opt-in names a host and port, so URL userinfo, paths and suffixes
	// accepted by other locator consumers cannot confer update authority.
	host, port, err := net.SplitHostPort(release.RemoteEndpoint)
	return err == nil && host != "" && port != "" &&
		!strings.ContainsAny(host, "/@?#\\ \t\r\n") && cfg.RemoteEndpoint == release.RemoteEndpoint
}

func (u clientUpdater) installedPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	executable, err := u.executable()
	if err != nil {
		return "", err
	}
	actual, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", err
	}
	path := filepath.Join(realHome, ".local", "bin", "wrkq")
	if actual != path {
		return "", errors.New("self-update requires the installed ~/.local/bin/wrkq")
	}
	return path, nil
}

func (u clientUpdater) command() *cobra.Command {
	return &cobra.Command{
		Use:   "self-update",
		Short: "Replace the installed wrkq with the configured wrkqd's client",
		Args:  cobra.NoArgs,
		RunE:  u.runManual,
	}
}

func (u clientUpdater) runManual(cmd *cobra.Command, _ []string) error {
	path, err := u.installedPath()
	if err != nil {
		return err
	}
	cfg, err := config.LoadWithDBOverride(dbOverride(cmd), false)
	if err != nil {
		return err
	}
	if cfg.RemoteEndpoint == "" {
		return errors.New("self-update requires an rpc:// database locator")
	}
	token := remoteTokenFromEnv()
	tr, target, handshakeErr := workrpcclient.NewRemoteWithIdentity(cmd.Context(), cfg.RemoteEndpoint, token, workrpcclient.WrkqProfile)
	var mismatch *workrpcclient.ProtocolMismatchError
	switch {
	case handshakeErr == nil:
		_ = tr.Close()
		if commitMatches(GitCommit, target.Revision) {
			fmt.Fprintf(cmd.OutOrStdout(), "already at %s\n", GitCommit)
			return nil
		}
	case errors.As(handshakeErr, &mismatch):
		target = mismatchIdentity(mismatch)
	default:
		return handshakeErr
	}
	version, err := updateInstalledClient(cmd, path, cfg.RemoteEndpoint, token, target, true)
	if err != nil {
		if handshakeErr != nil {
			printUpdateFailure(cmd, err)
			return handshakeErr
		}
		return fmt.Errorf("self-update did not happen: %w", err)
	}
	if version != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "already at %s\n", version)
	}
	return nil
}

func mismatchIdentity(err *workrpcclient.ProtocolMismatchError) workrpcclient.RemoteIdentity {
	return workrpcclient.RemoteIdentity{
		Revision: err.ServerRevision, ProtocolVersion: err.ServerProtocol,
		ProtocolSchemaHash: err.ServerSchemaHash,
	}
}

func commitMatches(commit, revision string) bool {
	return revisionPattern.MatchString(revision) && commitPattern.MatchString(commit) &&
		strings.HasPrefix(strings.ToLower(revision), strings.ToLower(commit))
}

func readClientVersion(ctx context.Context, path string) (clientVersion, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version", "--json").Output()
	if err != nil {
		return clientVersion{}, err
	}
	var version clientVersion
	if err := json.Unmarshal(out, &version); err != nil {
		return clientVersion{}, err
	}
	return version, nil
}

func printUpdateFailure(cmd *cobra.Command, err error) {
	fmt.Fprintf(cmd.ErrOrStderr(), "wrkq: self-update did not happen: %s\n", strings.Join(strings.Fields(err.Error()), " "))
}

func defaultClientUpdater() clientUpdater {
	return clientUpdater{executable: os.Executable, reexec: syscall.Exec}
}
