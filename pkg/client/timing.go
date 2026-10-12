package client

import (
	"crypto/tls"
	"io"
	"net/http/httptrace"
	"sync"
	"time"
)

// CallTiming is one remote workrpc round trip broken into client-observed
// phases. Encode, WroteAt, FirstByte and Total are offsets from Start; DNS,
// Connect and TLS are span lengths, zero when the phase did not happen (a
// reused keep-alive connection has none).
type CallTiming struct {
	Method    string
	Start     time.Time
	Encode    time.Duration // marshal params + request frame
	DNS       time.Duration
	Connect   time.Duration // TCP dial
	TLS       time.Duration
	Reused    bool          // keep-alive connection reused
	WroteAt   time.Duration // request fully written
	FirstByte time.Duration // first response byte (server work + wire)
	Total     time.Duration // response fully decoded
	ReqBytes  int
	RespBytes int64
	Err       error
}

var (
	observerMu sync.RWMutex
	observer   func(CallTiming)
)

// SetCallObserver installs fn to receive a CallTiming for every remote
// (rpc://) round trip, including rpc.initialize. nil removes it. Observation is
// off by default and costs nothing when unset.
func SetCallObserver(fn func(CallTiming)) {
	observerMu.Lock()
	observer = fn
	observerMu.Unlock()
}

func callObserver() func(CallTiming) {
	observerMu.RLock()
	defer observerMu.RUnlock()
	return observer
}

// callTrace builds the httptrace hooks that fill ct's network phases.
func callTrace(ct *CallTiming) *httptrace.ClientTrace {
	var dnsStart, connStart, tlsStart time.Time
	return &httptrace.ClientTrace{
		GotConn:  func(info httptrace.GotConnInfo) { ct.Reused = info.Reused },
		DNSStart: func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:  func(httptrace.DNSDoneInfo) { ct.DNS = time.Since(dnsStart) },
		ConnectStart: func(string, string) {
			connStart = time.Now()
		},
		ConnectDone:       func(string, string, error) { ct.Connect = time.Since(connStart) },
		TLSHandshakeStart: func() { tlsStart = time.Now() },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			ct.TLS = time.Since(tlsStart)
		},
		WroteRequest:         func(httptrace.WroteRequestInfo) { ct.WroteAt = time.Since(ct.Start) },
		GotFirstResponseByte: func() { ct.FirstByte = time.Since(ct.Start) },
	}
}

type countingReader struct {
	r io.Reader
	n *int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.n += int64(n)
	return n, err
}
