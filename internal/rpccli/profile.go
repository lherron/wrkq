package rpccli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	workrpcclient "github.com/lherron/wrkq/pkg/client"
)

// profileEnv opts a CLI invocation into a wall-time breakdown on stderr: how
// much went to process start, to each wrkqd round trip (split into connect,
// send, server+wire wait, and response read/decode), and to client-side work
// between calls. Remote (rpc://) transports only; stdout is untouched.
const profileEnv = "WRKQ_PROFILE"

type cliProfile struct {
	mu    sync.Mutex
	main  time.Time
	calls []workrpcclient.CallTiming
}

func startProfile() *cliProfile {
	if v := strings.TrimSpace(os.Getenv(profileEnv)); v == "" || v == "0" || v == "false" {
		return nil
	}
	p := &cliProfile{main: time.Now()}
	workrpcclient.SetCallObserver(func(ct workrpcclient.CallTiming) {
		p.mu.Lock()
		p.calls = append(p.calls, ct)
		p.mu.Unlock()
	})
	return p
}

func ms(d time.Duration) string { return fmt.Sprintf("%7.1fms", float64(d)/float64(time.Millisecond)) }

func (p *cliProfile) report(w io.Writer) {
	if p == nil {
		return
	}
	workrpcclient.SetCallObserver(nil)
	end := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()

	fmt.Fprintf(w, "\n%s: wall-time breakdown (%d RPC calls)\n", profileEnv, len(p.calls))
	var rpcTotal, wait, connect time.Duration
	if len(p.calls) > 0 {
		fmt.Fprintf(w, "  %-34s %-6s %9s %9s %9s %9s %9s %9s\n",
			"method", "conn", "connect", "send", "wait", "read", "total", "resp")
	}
	for _, c := range p.calls {
		conn := "new"
		if c.Reused {
			conn = "reuse"
		}
		send := c.WroteAt - c.Encode
		w8 := c.FirstByte - c.WroteAt
		read := c.Total - c.FirstByte
		if c.FirstByte == 0 { // failed before a response arrived
			w8, read = 0, 0
		}
		rpcTotal += c.Total
		wait += w8
		connect += c.DNS + c.Connect + c.TLS
		status := ""
		if c.Err != nil {
			status = "  ERR"
		}
		fmt.Fprintf(w, "  %-34s %-6s %s %s %s %s %s %8dB%s\n",
			c.Method, conn, ms(c.DNS+c.Connect+c.TLS), ms(send), ms(w8), ms(read), ms(c.Total), c.RespBytes, status)
	}

	inMain := end.Sub(p.main)
	fmt.Fprintln(w, "  ──")
	if start, ok := processStartTime(); ok && start.Before(p.main) {
		fmt.Fprintf(w, "  process start → main   %s  (exec, dyld, Go runtime + package init)\n", ms(p.main.Sub(start)))
		defer fmt.Fprintf(w, "  total since exec       %s\n", ms(end.Sub(start)))
	}
	fmt.Fprintf(w, "  main → exit            %s\n", ms(inMain))
	fmt.Fprintf(w, "    wrkqd round trips    %s  (connect %s, server+wire wait %s)\n", ms(rpcTotal), strings.TrimSpace(ms(connect)), strings.TrimSpace(ms(wait)))
	fmt.Fprintf(w, "    client-side          %s  (config, scoping, encode/decode, render)\n", ms(inMain-rpcTotal))
}
