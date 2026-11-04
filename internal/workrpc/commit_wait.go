//go:build wrkq_local

package workrpc

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/wrkqapi"
)

// registerCommitWait owns only ephemeral admission slots and an incarnation.
// The DB's existing post-commit broadcaster is the sole wake source.
func registerCommitWait(s *Server, database *db.DB) {
	incarnation := uuid.NewString()
	slots := make(chan struct{}, 512)
	s.Register("wrkq.commit.wait", apiHandler(func(ctx context.Context, p wrkqapi.CommitWaitParams) (any, error) {
		snapshot := func() (string, <-chan struct{}) {
			seq, changed := database.EventCommits.CoalescedSnapshot()
			return fmt.Sprintf("%s:%d", incarnation, seq), changed
		}
		token, changed := snapshot()
		result := func(changed bool) wrkqapi.WrkqCommitWait {
			return wrkqapi.WrkqCommitWait{Token: token, Changed: changed}
		}
		if p.Since != token {
			return result(true), nil
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			return result(false), nil
		}
		timeout := 25000
		if p.TimeoutMs != nil {
			timeout = *p.TimeoutMs
		}
		if timeout < 1000 {
			timeout = 1000
		}
		if timeout > 30000 {
			timeout = 30000
		}
		timer := time.NewTimer(time.Duration(timeout) * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
				// Preserve the input token on timeout: a racing commit remains visible
				// to the caller's next wait, even if it skips the read after a timeout.
				return result(false), nil
			case <-changed:
				next, pending := snapshot()
				if next != token {
					return wrkqapi.WrkqCommitWait{Token: next, Changed: true}, nil
				}
				changed = pending
			}
		}
	}))
}
