package wrkqapi

import (
	"context"
	"sync"
	"testing"

	"github.com/lherron/wrkq/internal/nodeauth"
)

func claimTestTask(t *testing.T, api *API, slug string) *WrkqTask {
	t.Helper()
	task, err := api.TaskCreate(context.Background(), TaskCreateParams{
		Title: slug, PrincipalRef: "agent:seed",
	})
	if err != nil {
		t.Fatalf("TaskCreate: %v", err)
	}
	return task
}

func nodeContext(node string) context.Context {
	return nodeauth.WithNode(context.Background(), node)
}

func claimErrorCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected claim error")
	}
	domainErr, ok := err.(Error)
	if !ok {
		t.Fatalf("error %T is not a wrkq domain error: %v", err, err)
	}
	return domainErr.Code()
}

func TestTaskClaimRequiresVerifiedNodeAndAtomicallySelectsOneWinner(t *testing.T) {
	api := newAttributionAPI(t, "agent:seed")
	task := claimTestTask(t, api, "claim-race")

	_, err := api.TaskClaim(context.Background(), TaskClaimParams{
		Task: task.ID, PrincipalRef: "agent:cody", Scope: "agent:cody:project:wrkq:task:" + task.ID,
	})
	if got := claimErrorCode(t, err); got != CodeNodeIdentity {
		t.Fatalf("missing-node code = %s, want %s", got, CodeNodeIdentity)
	}

	type result struct {
		claim *WrkqTaskClaim
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, contender := range []struct{ node, agent string }{{"lab", "cody"}, {"max3", "clod"}} {
		wg.Add(1)
		go func(node, agent string) {
			defer wg.Done()
			<-start
			claim, err := api.TaskClaim(nodeContext(node), TaskClaimParams{
				Task: task.ID, PrincipalRef: "agent:" + agent,
				Scope: "agent:" + agent + ":project:wrkq:task:" + task.ID,
			})
			results <- result{claim: claim, err: err}
		}(contender.node, contender.agent)
	}
	close(start)
	wg.Wait()
	close(results)

	winners, losers := 0, 0
	for got := range results {
		if got.err == nil {
			winners++
			if got.claim.ClaimGeneration != 1 || got.claim.ClaimToken == "" {
				t.Fatalf("first claim = %+v", got.claim)
			}
			continue
		}
		losers++
		if code := claimErrorCode(t, got.err); code != CodeAlreadyClaimed {
			t.Fatalf("loser code = %s, want %s", code, CodeAlreadyClaimed)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("race winners=%d losers=%d, want 1/1", winners, losers)
	}

	shown, err := api.TaskShow(context.Background(), TaskShowParams{Task: task.ID})
	if err != nil {
		t.Fatalf("TaskShow: %v", err)
	}
	if shown.ClaimedBy == "" || shown.ClaimedNode == "" || shown.ClaimGeneration != 1 || shown.State != "in_progress" {
		t.Fatalf("claimed task projection = %+v", shown)
	}

	wrongStateTask := claimTestTask(t, api, "wrong-state")
	completed := "completed"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: wrongStateTask.ID, Actor: "agent:seed", Patch: TaskPatch{State: &completed},
	}); err != nil {
		t.Fatalf("complete unclaimed task: %v", err)
	}
	_, err = api.TaskClaim(nodeContext("lab"), TaskClaimParams{
		Task: wrongStateTask.ID, PrincipalRef: "agent:cody",
		Scope: "agent:cody:project:wrkq:task:" + wrongStateTask.ID,
	})
	if got := claimErrorCode(t, err); got != CodeWrongState {
		t.Fatalf("terminal claim code = %s, want %s", got, CodeWrongState)
	}
}

func TestTaskClaimTakeoverFencesOldHolderButAllowsComments(t *testing.T) {
	api := newAttributionAPI(t, "agent:seed")
	task := claimTestTask(t, api, "takeover-fence")
	oldScope := "agent:cody:project:wrkq:task:" + task.ID
	oldClaim, err := api.TaskClaim(nodeContext("lab"), TaskClaimParams{
		Task: task.ID, PrincipalRef: "agent:cody", Scope: oldScope,
	})
	if err != nil {
		t.Fatalf("initial claim: %v", err)
	}
	newScope := "agent:clod:project:wrkq:task:" + task.ID
	newClaim, err := api.TaskClaim(nodeContext("max3"), TaskClaimParams{
		Task: task.ID, PrincipalRef: "agent:clod", Scope: newScope, TakeOver: true,
	})
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if newClaim.ClaimGeneration != oldClaim.ClaimGeneration+1 {
		t.Fatalf("takeover generation = %d, want %d", newClaim.ClaimGeneration, oldClaim.ClaimGeneration+1)
	}

	_, err = api.TaskClaimValidate(nodeContext("lab"), TaskClaimValidateParams{
		Task: task.ID, PrincipalRef: "agent:cody", Scope: oldScope,
		ClaimGeneration: oldClaim.ClaimGeneration, ClaimToken: oldClaim.ClaimToken,
	})
	if got := claimErrorCode(t, err); got != CodeClaimSuperseded {
		t.Fatalf("old validation code = %s, want %s", got, CodeClaimSuperseded)
	}

	completed := "completed"
	_, err = api.TaskUpdate(nodeContext("lab"), TaskUpdateParams{
		Task: task.ID, Actor: "agent:cody", Patch: TaskPatch{State: &completed},
		ClaimScope: oldScope, ClaimGeneration: oldClaim.ClaimGeneration, ClaimToken: oldClaim.ClaimToken,
	})
	if got := claimErrorCode(t, err); got != CodeClaimSuperseded {
		t.Fatalf("old completion code = %s, want %s", got, CodeClaimSuperseded)
	}

	if _, err := api.CommentAdd(context.Background(), CommentAddParams{
		Task: task.ID, Actor: "agent:cody", Body: "old holder diagnostic after takeover",
	}); err != nil {
		t.Fatalf("superseded holder comment: %v", err)
	}

	updated, err := api.TaskUpdate(nodeContext("max3"), TaskUpdateParams{
		Task: task.ID, Actor: "agent:clod", Patch: TaskPatch{State: &completed},
		ClaimScope: newScope, ClaimGeneration: newClaim.ClaimGeneration, ClaimToken: newClaim.ClaimToken,
	})
	if err != nil {
		t.Fatalf("current-holder completion: %v", err)
	}
	if updated.State != "completed" {
		t.Fatalf("state = %s, want completed", updated.State)
	}
}

func TestTaskReleasePreservesGenerationAndFiltersExposeHoldership(t *testing.T) {
	api := newAttributionAPI(t, "agent:seed")
	task := claimTestTask(t, api, "release-generation")
	scopeRef := "agent:cody:project:wrkq:task:" + task.ID
	claim, err := api.TaskClaim(nodeContext("lab"), TaskClaimParams{
		Task: task.ID, PrincipalRef: "agent:cody", Scope: scopeRef,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	list, err := api.TaskList(context.Background(), TaskListParams{ClaimedBy: "agent:cody", ClaimedNode: "lab"})
	if err != nil || len(list.Items) != 1 || list.Items[0].ID != task.ID {
		t.Fatalf("claim filters: list=%+v err=%v", list, err)
	}
	find, err := api.FindListView(context.Background(), FindListViewParams{
		State: "all", ClaimedBy: "agent:cody", ClaimedNode: "lab",
	})
	if err != nil || len(find.Items) != 1 || find.Items[0].ClaimedBy == nil || find.Items[0].ClaimedNode == nil || *find.Items[0].ClaimedNode != "lab" {
		t.Fatalf("find holdership projection: find=%+v err=%v", find, err)
	}
	cat, err := api.TaskCatView(context.Background(), TaskCatViewParams{Task: task.ID})
	if err != nil || cat.ClaimedBy == nil || *cat.ClaimedBy != "agent:cody" || cat.ClaimGeneration != 1 {
		t.Fatalf("cat holdership projection: cat=%+v err=%v", cat, err)
	}
	if _, err := api.TaskRelease(nodeContext("lab"), TaskReleaseParams{
		Task: task.ID, PrincipalRef: "agent:cody", Scope: scopeRef,
		ClaimGeneration: claim.ClaimGeneration, ClaimToken: claim.ClaimToken,
	}); err != nil {
		t.Fatalf("release: %v", err)
	}
	shown, err := api.TaskShow(context.Background(), TaskShowParams{Task: task.ID})
	if err != nil {
		t.Fatalf("show after release: %v", err)
	}
	if shown.ClaimedBy != "" || shown.ClaimGeneration != claim.ClaimGeneration || shown.State != "in_progress" {
		t.Fatalf("released task = %+v", shown)
	}
	if _, err := api.db.Exec("UPDATE tasks SET claim_generation = claim_generation - 1 WHERE id = ?", task.ID); err == nil {
		t.Fatal("claim generation regression should be rejected by durable schema")
	}
	if _, err := api.db.Exec("UPDATE tasks SET claimed_by_principal_ref = 'agent:cody' WHERE id = ?", task.ID); err == nil {
		t.Fatal("partial claim tuple should be rejected by durable schema")
	}
	reclaimed, err := api.TaskClaim(nodeContext("max3"), TaskClaimParams{
		Task: task.ID, PrincipalRef: "agent:clod", Scope: "agent:clod:project:wrkq:task:" + task.ID,
	})
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if reclaimed.ClaimGeneration != claim.ClaimGeneration+1 {
		t.Fatalf("reclaim generation = %d, want %d", reclaimed.ClaimGeneration, claim.ClaimGeneration+1)
	}
}
