package wrkqd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lherron/wrkq/internal/workrpc"
)

// One JSON line per HTTP request plus an http.listen startup line on stdout
// (logging L3; launchd routes it to
// var/logs/wrkq/wrkqd.log, diagnostics stay on stderr in wrkqd.err.log), in the
// `{level, component, event, data, ts}` shape acp-server's request-log.ts
// writes. Every line carries `listener`, the matched `route` template and the
// caller's `client` token. Never logged: bodies, query strings, headers; for a
// non-2xx JSON response only the error `code` and `message` are lifted. A
// /v1/rpc line also names its JSON-RPC method as `op`, and a frame that failed inside an
// HTTP 200 logs as a warn with the JSON-RPC error's code and message.
//
// Follow loops (wrkp log --follow, wrkc monitor, wrkf watch) poll every
// 100ms, about 10 lines a second per follower. As with acp's quiet polls,
// a failing poll always logs; a successful one logs at most once per
// sample window (default one minute, --poll-sample-window) per client,
// remote and op, with `suppressed` counting
// the successes skipped since the last line, so the caller stays visible.

const (
	listenerMain   = "main"
	listenerUnix   = "unix"
	unmatchedRoute = "unmatched"
	// Error bodies are small; this bounds what the log lift buffers.
	maxLiftedErrorBody = 4096
	// DefaultPollSampleWindow is the --poll-sample-window default.
	DefaultPollSampleWindow = time.Minute
)

// sampledPollOps is the L3 sampled set: ops whose successes log at most once
// per sample window. rpc.initialize is every CLI process's handshake before
// its real call, which carries the same client; envelope.pendingView is the
// hook-driven inbox poll.
var sampledPollOps = map[string]bool{
	"rpc.initialize":              true,
	"wrkq.envelope.pendingView":   true,
	"wrkq.container.timelineView": true,
	"wrkq.container.taskCounts":   true,
	"wrkq.monitor.eventsView":     true,
	"wrkq.monitor.stateView":      true,
	"wrkq.history.tailView":       true,
	"wrkf.watch.events":           true,
	"wrkf.watch.snapshot":         true,
}

// requestLogOut is the process's real stdout, captured before Run points
// os.Stdout at stderr to keep stray handler prints out of the request log.
var requestLogOut io.Writer = os.Stdout

var clientToken = regexp.MustCompile(`^([A-Za-z0-9._~+-]{1,64})/([A-Za-z0-9._~+-]{1,64})`)

type requestLogLine struct {
	Level     string `json:"level"`
	Component string `json:"component"`
	Event     string `json:"event"`
	Data      any    `json:"data"`
	TS        string `json:"ts"`
}

type requestLogField struct {
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	Route      string  `json:"route"`
	Listener   string  `json:"listener"`
	Client     string  `json:"client"`
	RequestID  string  `json:"requestId,omitempty"`
	Status     int     `json:"status"`
	DurationMs int64   `json:"durationMs"`
	Remote     *string `json:"remote"`
	Op         string  `json:"op,omitempty"`
	Suppressed int     `json:"suppressed,omitempty"`
	Code       any     `json:"code,omitempty"`
	Message    string  `json:"message,omitempty"`
}

type rpcLogKey struct{}

// rpcLogNote is filled by the /v1/rpc handler for the request log to read.
type rpcLogNote struct {
	method string
	err    *workrpc.RPCError
}

func noteRPC(ctx context.Context, method string, rpcErr *workrpc.RPCError) {
	if note, ok := ctx.Value(rpcLogKey{}).(*rpcLogNote); ok {
		note.method, note.err = method, rpcErr
	}
}

type quietPollKey struct{ client, remote, op string }

type quietPollState struct {
	mu     sync.Mutex
	window time.Duration
	last   map[quietPollKey]time.Time
	skip   map[quietPollKey]int
}

var quietPolls = quietPollState{
	window: DefaultPollSampleWindow,
	last:   map[quietPollKey]time.Time{},
	skip:   map[quietPollKey]int{},
}

// admit reports whether this successful poll logs, and how many it stands for.
func (q *quietPollState) admit(key quietPollKey, now time.Time) (bool, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if last, ok := q.last[key]; ok && now.Sub(last) < q.window {
		q.skip[key]++
		return false, 0
	}
	skipped := q.skip[key]
	q.last[key] = now
	delete(q.skip, key)
	return true, skipped
}

// withRequestLog wraps the mux. ServeMux records the matched pattern on the
// request it was handed, so r.Pattern is readable once next returns.
func withRequestLog(next http.Handler, listener string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		note := &rpcLogNote{}
		r = r.WithContext(context.WithValue(r.Context(), rpcLogKey{}, note))
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := r.Pattern
		if route == "" {
			route = unmatchedRoute
		}
		field := requestLogField{
			Method:     r.Method,
			Path:       r.URL.Path,
			Route:      route,
			Listener:   listener,
			Client:     clientFromUserAgent(r.UserAgent()),
			RequestID:  truncate(r.Header.Get("X-Request-ID")),
			Status:     rec.status,
			DurationMs: time.Since(start).Milliseconds(),
			Remote:     remoteHost(r.RemoteAddr),
			Op:         note.method,
		}
		level := "info"
		switch {
		case rec.status < 200 || rec.status >= 300:
			level = "warn"
			if rec.status >= 500 {
				level = "error"
			}
			field.Code, field.Message = liftError(rec.body.Bytes())
		case note.err != nil:
			level = "warn"
			field.Code, field.Message = note.err.Code, truncate(note.err.Message)
		case sampledPollOps[note.method]:
			remote := ""
			if field.Remote != nil {
				remote = *field.Remote
			}
			ok, skipped := quietPolls.admit(quietPollKey{field.Client, remote, note.method}, start)
			if !ok {
				return
			}
			field.Suppressed = skipped
		}
		writeHTTPLogLine(requestLogLine{
			Level:     level,
			Component: "http",
			Event:     "http.request",
			Data:      field,
			TS:        time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	})
}

// clientFromUserAgent is the leading `<name>/<version>` product token, else
// `unknown`.
func clientFromUserAgent(userAgent string) string {
	m := clientToken.FindStringSubmatch(strings.TrimSpace(userAgent))
	if m == nil {
		return "unknown"
	}
	return m[1] + "/" + m[2]
}

func remoteHost(addr string) *string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		// Unix-socket peers have no address.
		return nil
	}
	return &host
}

// liftError reads the two error shapes wrkqd writes: `{message}` and a
// JSON-RPC `{error: {code, message}}`.
func liftError(body []byte) (any, string) {
	var record struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
		Error   *struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if len(body) == 0 || json.Unmarshal(body, &record) != nil {
		return nil, ""
	}
	code, message := record.Code, record.Message
	if record.Error != nil {
		code, message = record.Error.Code, record.Error.Message
	}
	return code, truncate(message)
}

func truncate(message string) string {
	if len(message) > 500 {
		return message[:500]
	}
	return message
}

func writeHTTPLogLine(line requestLogLine) {
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	// One write per line keeps concurrent requests from interleaving.
	_, _ = requestLogOut.Write(append(b, '\n'))
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if (r.status < 200 || r.status >= 300) && r.body.Len() < maxLiftedErrorBody {
		r.body.Write(p[:min(len(p), maxLiftedErrorBody-r.body.Len())])
	}
	return r.ResponseWriter.Write(p)
}
