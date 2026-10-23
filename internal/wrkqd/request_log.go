package wrkqd

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// One JSON line per HTTP request into wrkqd.log (logging L3), in the
// `{level, component, event, data, ts}` shape acp-server's request-log.ts
// writes. Every line carries `listener`, the matched `route` template and the
// caller's `client` token. Never logged: bodies, query strings, headers; for a
// non-2xx JSON response only the error `code` and `message` are lifted.

const (
	listenerMain   = "main"
	listenerUnix   = "unix"
	unmatchedRoute = "unmatched"
	// Error bodies are small; this bounds what the log lift buffers.
	maxLiftedErrorBody = 4096
)

var clientToken = regexp.MustCompile(`^([A-Za-z0-9._~+-]{1,64})/([A-Za-z0-9._~+-]{1,64})`)

type requestLogLine struct {
	Level     string          `json:"level"`
	Component string          `json:"component"`
	Event     string          `json:"event"`
	Data      requestLogField `json:"data"`
	TS        string          `json:"ts"`
}

type requestLogField struct {
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	Route      string  `json:"route"`
	Listener   string  `json:"listener"`
	Client     string  `json:"client"`
	Status     int     `json:"status"`
	DurationMs int64   `json:"durationMs"`
	Remote     *string `json:"remote"`
	Code       any     `json:"code,omitempty"`
	Message    string  `json:"message,omitempty"`
}

// withRequestLog wraps the mux. ServeMux records the matched pattern on the
// request it was handed, so r.Pattern is readable once next returns.
func withRequestLog(next http.Handler, listener string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
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
			Status:     rec.status,
			DurationMs: time.Since(start).Milliseconds(),
			Remote:     remoteHost(r.RemoteAddr),
		}
		level := "info"
		if rec.status < 200 || rec.status >= 300 {
			level = "warn"
			if rec.status >= 500 {
				level = "error"
			}
			field.Code, field.Message = liftError(rec.body.Bytes())
		}
		writeRequestLogLine(requestLogLine{
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
	if len(message) > 500 {
		message = message[:500]
	}
	return code, message
}

func writeRequestLogLine(line requestLogLine) {
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	// One write per line keeps concurrent requests from interleaving in
	// wrkqd.log, which launchd appends both stdout and stderr to.
	_, _ = os.Stderr.Write(append(b, '\n'))
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
