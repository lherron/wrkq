# Public Go client

Import `github.com/lherron/wrkq/pkg/client`. The package name is `client`; callers
may alias it to `wrkq` for the compact API shown below:

```go
import wrkq "github.com/lherron/wrkq/pkg/client"

api, err := wrkq.New(ctx, wrkq.WithPrincipalRef("agent:hcs"))
if err != nil {
	return err
}
defer api.Close()

task, err := api.Task.Show("T-07729")
receipt, err := api.Room.Say("T-07729", "implementation complete", wrkq.RoomSayOptions{
	To:      []string{"mable@hcs:primary"},
	Wait:    true,
	Timeout: 10 * time.Minute,
})
```

`New` uses the same `WRKQ_DB`, dotenv/YAML, `WRKQD_TOKEN`, and
`WRKQD_TOKEN_FILE` precedence as the CLIs. `WithPrincipalRef` is the equivalent
of `--as`; it accepts either `agent:<id>` or a full agent ScopeRef. The default
portable build connects to an `rpc://` locator. Local SQLite operation requires
the `wrkq_local` build tag.

The package owns the same fail-closed transport used by `wrkq`, `wrkc`, and
`wrkf`: initialization checks the complete protocol schema hash before any
business call. `Client.Call` exposes the typed escape hatch for registered
methods, while `Task`, `Comment`, `Promise`, `Container`, and `Room` provide the
external coordinator surface.
