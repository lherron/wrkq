package rpccli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/lherron/wrkq/internal/useragent"
	workrpcclient "github.com/lherron/wrkq/pkg/client"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

// updateInstalledClient returns the installed commit when a caller has already
// put a compatible binary in place. The manual verb also requires that this
// installed binary be from the requested revision.
func updateInstalledClient(cmd *cobra.Command, path, endpoint, token string, target workrpcclient.RemoteIdentity, manual bool) (string, error) {
	alreadyAt, previous, err := swapInstalledClient(cmd.Context(), path, endpoint, token, target, manual)
	if err == nil && previous != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "wrkq: self-updated ~/.local/bin/wrkq %s -> %s to match wrkqd; previous kept as wrkq.bak\n",
			previous, target.Revision)
	}
	return alreadyAt, err
}

func swapInstalledClient(ctx context.Context, path, endpoint, token string, target workrpcclient.RemoteIdentity, manual bool) (alreadyAt, previous string, err error) {
	if !revisionPattern.MatchString(target.Revision) {
		return "", "", errors.New("wrkqd did not report a full Git revision")
	}
	lockCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	unlock, err := lockClientUpdate(lockCtx, filepath.Dir(path))
	if err != nil {
		return "", "", err
	}
	defer unlock()

	old, err := readClientVersion(ctx, path)
	if err != nil || !commitPattern.MatchString(old.Commit) {
		old.Commit = "unknown"
	}
	if old.ProtocolVersion != "" && old.ProtocolSchemaHash != "" &&
		old.ProtocolVersion == target.ProtocolVersion && old.ProtocolSchemaHash == target.ProtocolSchemaHash &&
		(!manual || commitMatches(old.Commit, target.Revision)) {
		return old.Commit, "", nil
	}

	tmp := path + ".new." + strconv.Itoa(os.Getpid())
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return "", "", fmt.Errorf("create replacement: %w", err)
	}
	defer func() { _ = os.Remove(tmp) }()
	fetchErr := fetchClient(ctx, endpoint, token, target.Revision, f)
	modeErr := f.Chmod(0o755)
	closeErr := f.Close()
	if err := errors.Join(fetchErr, modeErr, closeErr); err != nil {
		return "", "", err
	}
	newVersion, err := readClientVersion(ctx, tmp)
	if err != nil {
		return "", "", fmt.Errorf("read downloaded client version: %w", err)
	}
	if !commitMatches(newVersion.Commit, target.Revision) {
		return "", "", errors.New("downloaded client's commit did not match wrkqd")
	}
	if err := backUpClient(path); err != nil {
		return "", "", fmt.Errorf("back up installed client: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", "", fmt.Errorf("replace installed client: %w", err)
	}
	return "", old.Commit, nil
}

func lockClientUpdate(ctx context.Context, dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, ".wrkq-self-update.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open self-update lock: %w", err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("wait for self-update lock: %w", err)
		}
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			_ = f.Close()
			return nil, fmt.Errorf("lock self-update: %w", err)
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func fetchClient(ctx context.Context, endpoint, token, revision string, dst io.Writer) error {
	query := url.Values{"os": {runtime.GOOS}, "arch": {runtime.GOARCH}}
	target := url.URL{Scheme: "http", Host: endpoint, Path: "/v1/client/wrkq", RawQuery: query.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return fmt.Errorf("create client download request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("User-Agent", useragent.String())
	req.Header.Set("X-Praesidium-Caller", useragent.ResolveCaller(""))
	client := &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download client: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return errors.New("download client: client_unavailable (HTTP 404)")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download client: HTTP %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Wrkq-Revision") != revision {
		return errors.New("download revision header did not match wrkqd")
	}
	if _, err := io.Copy(dst, resp.Body); err != nil {
		return fmt.Errorf("write downloaded client: %w", err)
	}
	return nil
}

func backUpClient(path string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	dst, err := os.CreateTemp(filepath.Dir(path), ".wrkq.bak.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(dst.Name()) }()
	_, copyErr := io.Copy(dst, src)
	modeErr := dst.Chmod(info.Mode())
	closeErr := dst.Close()
	if err := errors.Join(copyErr, modeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(dst.Name(), path+".bak")
}
