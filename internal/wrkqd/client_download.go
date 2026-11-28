package wrkqd

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"

	"github.com/lherron/wrkq/internal/nodeauth"
)

var platformSelector = regexp.MustCompile(`^[a-z0-9]+$`)

func (s *daemonServer) handleClientDownload(w http.ResponseWriter, r *http.Request) {
	if note, ok := r.Context().Value(rpcLogKey{}).(*rpcLogNote); ok {
		note.node, _ = nodeauth.FromContext(r.Context())
	}
	if !s.allowMethod(w, r, http.MethodGet) {
		return
	}
	unavailable := func() {
		s.writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "client_unavailable", "message": "wrkq client unavailable for this revision and platform",
		})
	}
	query := r.URL.Query()
	goos, arch := query.Get("os"), query.Get("arch")
	if GitCommit == "" || GitCommit == "unknown" ||
		!platformSelector.MatchString(goos) || !platformSelector.MatchString(arch) ||
		goos != runtime.GOOS || arch != runtime.GOARCH {
		unavailable()
		return
	}
	clientsDir := os.Getenv("WRKQD_CLIENTS_DIR")
	if clientsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			unavailable()
			return
		}
		clientsDir = filepath.Join(home, ".local", "share", "wrkq", "clients")
	}
	path := filepath.Join(clientsDir, GitCommit, "wrkq-"+goos+"-"+arch)
	f, err := os.Open(path)
	if err != nil {
		unavailable()
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		unavailable()
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Wrkq-Revision", GitCommit)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}
