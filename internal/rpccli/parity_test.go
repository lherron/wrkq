package rpccli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// TestParity is the data-driven old-vs-new equivalence harness. It is the single
// proof that `wrkq-rpccli <cmd>` is functionally equivalent to legacy `wrkq <cmd>`:
// for each case it seeds two identical fixtures, runs the OLD binary on one and
// the NEW binary on the other, and asserts byte-equal exit code + stdout + stderr,
// plus an identical durable-task-table snapshot for mutating commands.
//
// Adding coverage for a new command means appending rows to parityCases ✓ never
// editing the driver. This is the migration's command-equivalence oracle; the
// old CLI is the source of truth (rpcwrkqcli.md "Compatibility Invariant").
type parityCase struct {
	name string
	// setup is a sequence of legacy-wrkq argv (excluding --db/--as) run to seed
	// the fixture identically for both binaries.
	setup [][]string
	// args is the command under test, run on BOTH binaries (excluding --db/--as).
	args []string
	// mutates additionally compares the durable task-table snapshot.
	mutates bool
	// normalizeUUID neutralizes UUIDs in stdout/stderr before comparison. Set for
	// CREATE commands whose output echoes a freshly-generated (non-deterministic)
	// UUID; leave false for reads so a wrong-UUID bug is still caught.
	normalizeUUID bool
	// env adds extra environment variables (beyond the hermetic HOME/PATH) for the
	// command-under-test run on BOTH binaries — e.g. WRKQ_PROJECT_ROOT/ASP_PROJECT
	// to prove project-root scoping. The setup/seed always runs root-less.
	env []string
}

var parityCases = []parityCase{
	{
		name:    "ack/fresh",
		setup:   [][]string{{"touch", "inbox/done", "-t", "done"}, {"set", "inbox/done", "--state", "completed"}},
		args:    []string{"ack", "inbox/done"},
		mutates: true,
	},
	{
		name: "ack/skip-already-acked",
		setup: [][]string{
			{"touch", "inbox/done", "-t", "done"}, {"set", "inbox/done", "--state", "completed"}, {"ack", "inbox/done"},
		},
		args:    []string{"ack", "inbox/done"},
		mutates: true,
	},
	{
		name:    "ack/nonterminal-noforce-errors",
		setup:   [][]string{{"touch", "inbox/open", "-t", "open"}},
		args:    []string{"ack", "inbox/open"},
		mutates: false,
	},
	{
		name:    "ack/force-on-open",
		setup:   [][]string{{"touch", "inbox/open", "-t", "open"}},
		args:    []string{"ack", "inbox/open", "--force"},
		mutates: true,
	},
	{
		name:    "ack/unknown-ref-errors",
		setup:   nil,
		args:    []string{"ack", "T-09999999"},
		mutates: false,
	},
	{
		name: "ack/multi-mixed-fresh-and-skip",
		setup: [][]string{
			{"touch", "inbox/task-a", "-t", "a"}, {"set", "inbox/task-a", "--state", "completed"}, {"ack", "inbox/task-a"},
			{"touch", "inbox/task-b", "-t", "b"}, {"set", "inbox/task-b", "--state", "completed"},
		},
		args:    []string{"ack", "inbox/task-a", "inbox/task-b"},
		mutates: true,
	},

	// stat ✓ first RPC-backed READ command (re-projects wrkq.task.show /
	// wrkq.container.show into the legacy stat metadata shape). Read-only.
	{
		name:  "stat/task",
		setup: [][]string{{"touch", "inbox/thing", "-t", "A Thing", "--priority", "2"}},
		args:  []string{"stat", "inbox/thing"},
	},
	{
		name:  "stat/task-by-id",
		setup: [][]string{{"touch", "inbox/thing", "-t", "A Thing"}},
		args:  []string{"stat", "T-00001"},
	},
	{
		name:  "stat/container",
		setup: [][]string{{"mkdir", "proj"}},
		args:  []string{"stat", "proj"},
	},
	{
		name:  "stat/multi-task-and-container",
		setup: [][]string{{"mkdir", "proj"}, {"touch", "proj/thing", "-t", "T"}},
		args:  []string{"stat", "proj", "proj/thing"},
	},
	{
		name:  "stat/unknown-ref-errors",
		setup: nil,
		args:  []string{"stat", "T-09999999"},
	},
	{
		name:  "stat/completed-task",
		setup: [][]string{{"touch", "inbox/done", "-t", "Done"}, {"set", "inbox/done", "--state", "completed"}},
		args:  []string{"stat", "inbox/done"},
	},

	// cat --json ✓ RPC-backed via wrkq.task.catView (server-owned compat projection).
	// JSON mode only (ndjson/porcelain/raw are not yet implemented; cat is partial).
	{
		name:  "cat/single-json",
		setup: [][]string{{"touch", "inbox/one", "-t", "One", "--priority", "2", "--labels", `["x","y"]`, "-d", "body Ã¢ÂÂ"}},
		args:  []string{"cat", "inbox/one", "--json"},
	},
	{
		name:  "cat/multi-json",
		setup: [][]string{{"touch", "inbox/one", "-t", "One"}, {"touch", "inbox/two", "-t", "Two"}},
		args:  []string{"cat", "inbox/one", "inbox/two", "--json"},
	},
	{
		name: "cat/with-comment",
		setup: [][]string{
			{"touch", "inbox/cmt", "-t", "Commented"}, {"comment", "add", "inbox/cmt", "first comment Ã¢ÂÂ"},
		},
		args: []string{"cat", "inbox/cmt", "--json"},
	},
	{
		name: "cat/exclude-comments",
		setup: [][]string{
			{"touch", "inbox/cmt", "-t", "Commented"}, {"comment", "add", "inbox/cmt", "hidden"},
		},
		args: []string{"cat", "inbox/cmt", "--json", "--exclude-comments"},
	},
	{
		name: "cat/with-relation-and-blocker",
		setup: [][]string{
			{"touch", "inbox/main", "-t", "Main"},
			{"touch", "inbox/blk", "-t", "Blocker"},
			{"relation", "add", "inbox/blk", "blocks", "inbox/main"},
		},
		args: []string{"cat", "inbox/main", "--json"},
	},
	{
		name:  "cat/unknown-ref-errors",
		setup: nil,
		args:  []string{"cat", "T-09999999", "--json"},
	},

	// mkdir / rmdir ✓ RPC-backed via wrkq.container.create / .delete(Recursive).
	{
		name:    "mkdir/single",
		setup:   nil,
		args:    []string{"mkdir", "proj"},
		mutates: true,
	},
	{
		name:    "mkdir/multi",
		setup:   nil,
		args:    []string{"mkdir", "proj", "area"},
		mutates: true,
	},
	{
		name:    "rmdir/empty",
		setup:   [][]string{{"mkdir", "gone"}},
		args:    []string{"rmdir", "gone"},
		mutates: true,
	},
	// NOTE: `rmdir --force` (recursive) is intentionally NOT covered yet ✓ legacy
	// uses an interactive confirmation flow and the RPC deleteRecursive requires an
	// "expected impact" confirmation param. That reconciliation is a tracked gap.

	// touch ✓ RPC-backed via wrkq.task.create, re-projected to the legacy
	// touchResult array. Core flags only (due-at/start-at/etc. hard-error as gaps).
	{
		name:          "touch/basic",
		setup:         nil,
		args:          []string{"touch", "inbox/newtask", "-t", "New Task"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		name:          "touch/flags",
		setup:         nil,
		args:          []string{"touch", "inbox/rich", "-t", "Rich Ã¢ÂÂ", "--priority", "1", "--kind", "bug", "-d", "desc", "--labels", `["a","b"]`},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		name:          "touch/default-title-is-slug",
		setup:         nil,
		args:          []string{"touch", "inbox/noname"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		name:    "touch/duplicate-errors",
		setup:   [][]string{{"touch", "inbox/dup", "-t", "Dup"}},
		args:    []string{"touch", "inbox/dup", "-t", "Dup"},
		mutates: false,
	},

	// mv â single-source task move into an existing container (wrkq.task.move).
	{
		name:    "mv/task-into-container",
		setup:   [][]string{{"touch", "inbox/movable", "-t", "M"}, {"mkdir", "dest"}},
		args:    []string{"mv", "inbox/movable", "dest"},
		mutates: true,
	},

	// set — field updates via wrkq.task.update patch (success cases).
	{
		name:    "set/state",
		setup:   [][]string{{"touch", "inbox/s1", "-t", "S1"}},
		args:    []string{"set", "inbox/s1", "--state", "in_progress"},
		mutates: true,
	},
	{
		name:    "set/multi-field",
		setup:   [][]string{{"touch", "inbox/s2", "-t", "S2"}},
		args:    []string{"set", "inbox/s2", "--priority", "1", "--title", "New Title", "--labels", `["z"]`},
		mutates: true,
	},

	// comment add via wrkq.comment.add (re-projected to legacy snake_case output).
	{
		name:          "comment/add",
		setup:         [][]string{{"touch", "inbox/ct", "-t", "CT"}},
		args:          []string{"comment", "add", "inbox/ct", "hello world"},
		mutates:       true,
		normalizeUUID: true,
	},

	// relation add/rm via wrkq.relation.add/.remove (composes task.show for id+uuid).
	{
		name:    "relation/add",
		setup:   [][]string{{"touch", "inbox/ra", "-t", "A"}, {"touch", "inbox/rb", "-t", "B"}},
		args:    []string{"relation", "add", "inbox/ra", "blocks", "inbox/rb"},
		mutates: true,
	},
	{
		name:    "relation/rm",
		setup:   [][]string{{"touch", "inbox/ra", "-t", "A"}, {"touch", "inbox/rb", "-t", "B"}, {"relation", "add", "inbox/ra", "blocks", "inbox/rb"}},
		args:    []string{"relation", "rm", "inbox/ra", "blocks", "inbox/rb"},
		mutates: true,
	},

	// relation ls via wrkq.relation.listView (server compat list projection).
	{
		name:  "relation-ls/json",
		setup: [][]string{{"touch", "inbox/ra", "-t", "A"}, {"touch", "inbox/rb", "-t", "B"}, {"relation", "add", "inbox/ra", "blocks", "inbox/rb"}},
		args:  []string{"relation", "ls", "inbox/ra", "--json"},
	},
	{
		name:  "relation-ls/ndjson-default",
		setup: [][]string{{"touch", "inbox/ra", "-t", "A"}, {"touch", "inbox/rb", "-t", "B"}, {"relation", "add", "inbox/ra", "blocks", "inbox/rb"}},
		args:  []string{"relation", "ls", "inbox/rb"},
	},
	{
		name:  "relation-ls/empty",
		setup: [][]string{{"touch", "inbox/ra", "-t", "A"}},
		args:  []string{"relation", "ls", "inbox/ra", "--json"},
	},

	// container cat via wrkq.container.catView (server compat projection).
	{
		name:  "container-cat/project",
		setup: [][]string{{"mkdir", "myproj"}},
		args:  []string{"container", "cat", "myproj", "--json"},
	},
	{
		name:  "container-cat/nested-rich",
		setup: [][]string{{"mkdir", "realproj"}, {"mkdir", "realproj/sub"}},
		args:  []string{"container", "cat", "realproj/sub", "--json"},
	},
	{
		name:  "comment-cat/by-id",
		setup: [][]string{{"touch", "inbox/cc", "-t", "CC"}, {"comment", "add", "inbox/cc", "the body"}},
		args:  []string{"comment", "cat", "C-00001", "--json"},
	},
	{
		name:  "comment-ls/json",
		setup: [][]string{{"touch", "inbox/cl", "-t", "CL"}, {"comment", "add", "inbox/cl", "one"}, {"comment", "add", "inbox/cl", "two"}},
		args:  []string{"comment", "ls", "inbox/cl", "--json"},
	},
	{
		name:  "comment-ls/ndjson-default",
		setup: [][]string{{"touch", "inbox/cl", "-t", "CL"}, {"comment", "add", "inbox/cl", "one"}, {"comment", "add", "inbox/cl", "two"}},
		args:  []string{"comment", "ls", "inbox/cl"},
	},
	{
		name:  "comment-ls/porcelain-limit-cursor",
		setup: [][]string{{"touch", "inbox/cl", "-t", "CL"}, {"comment", "add", "inbox/cl", "one"}, {"comment", "add", "inbox/cl", "two"}, {"comment", "add", "inbox/cl", "three"}},
		args:  []string{"comment", "ls", "inbox/cl", "--porcelain", "--limit", "2"},
	},
	{
		name:  "comment-ls/empty",
		setup: [][]string{{"touch", "inbox/cl", "-t", "CL"}},
		args:  []string{"comment", "ls", "inbox/cl", "--json"},
	},
	{
		name:  "comment-ls/sort-id",
		setup: [][]string{{"touch", "inbox/cl", "-t", "CL"}, {"comment", "add", "inbox/cl", "one"}, {"comment", "add", "inbox/cl", "two"}},
		args:  []string{"comment", "ls", "inbox/cl", "--json", "--sort", "id"},
	},
	{
		name:  "comment-ls/sort-created_at",
		setup: [][]string{{"touch", "inbox/cl", "-t", "CL"}, {"comment", "add", "inbox/cl", "one"}, {"comment", "add", "inbox/cl", "two"}},
		args:  []string{"comment", "ls", "inbox/cl", "--json", "--sort", "created_at"},
	},
	{
		name:  "comment-ls/sort-updated_at",
		setup: [][]string{{"touch", "inbox/cl", "-t", "CL"}, {"comment", "add", "inbox/cl", "one"}, {"comment", "add", "inbox/cl", "two"}},
		args:  []string{"comment", "ls", "inbox/cl", "--json", "--sort", "updated_at"},
	},
	{
		name:  "comment-ls/malformed-cursor-errors",
		setup: [][]string{{"touch", "inbox/cl", "-t", "CL"}},
		args:  []string{"comment", "ls", "inbox/cl", "--json", "--cursor", "not-a-valid-cursor"},
	},
	{
		name:  "comment-ls/include-deleted-none",
		setup: [][]string{{"touch", "inbox/cl", "-t", "CL"}, {"comment", "add", "inbox/cl", "one"}},
		args:  []string{"comment", "ls", "inbox/cl", "--json", "--include-deleted"},
	},

	// attach ls (empty) via wrkq.attachment.listView (cursor pattern; populated case needs attach put fs, pending).
	{
		name:  "attach-ls/empty",
		setup: [][]string{{"touch", "inbox/at", "-t", "AT"}},
		args:  []string{"attach", "ls", "inbox/at", "--json"},
	},

	// ls via wrkq.task.lsView (server compat list projection: mixed task/container
	// listing, recursive rollup counts, in-memory merge-sort, cursor over the
	// merged set). lsMixed seeds a project with child containers + tasks.
	{
		name:  "ls/top-level-rollups",
		setup: lsMixed,
		args:  []string{"ls", "--json"},
	},
	{
		name:  "ls/container-mixed",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--json"},
	},
	{
		name:  "ls/ndjson-default",
		setup: lsMixed,
		args:  []string{"ls", "proj"},
	},
	{
		name:  "ls/porcelain-limit-cursor",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--porcelain", "--limit", "2"},
	},
	{
		name:  "ls/sort-id",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--json", "--sort", "id"},
	},
	{
		name:  "ls/sort-created_at",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--json", "--sort", "created_at"},
	},
	{
		name:  "ls/sort-updated_at",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--json", "--sort", "updated_at"},
	},
	{
		name:  "ls/reverse",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--json", "--reverse"},
	},
	{
		name:  "ls/type-p",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--json", "--type", "p"},
	},
	{
		name:  "ls/type-t",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--json", "--type", "t"},
	},
	{
		name: "ls/all-includes-hidden",
		setup: append(append([][]string{}, lsMixed...),
			[]string{"touch", "proj/done", "-t", "Done"}, []string{"set", "proj/done", "--state", "completed"}),
		args: []string{"ls", "proj", "--json", "--all"},
	},
	{
		name: "ls/default-hides-completed",
		setup: append(append([][]string{}, lsMixed...),
			[]string{"touch", "proj/done", "-t", "Done"}, []string{"set", "proj/done", "--state", "completed"}),
		args: []string{"ls", "proj", "--json"},
	},
	{
		name:  "ls/single-task",
		setup: lsMixed,
		args:  []string{"ls", "proj/task-x", "--json"},
	},
	{
		name:  "ls/empty-null",
		setup: [][]string{{"mkdir", "empty"}},
		args:  []string{"ls", "empty", "--json"},
	},
	{
		name:  "ls/unknown-path-errors",
		setup: nil,
		args:  []string{"ls", "nope", "--json"},
	},
	{
		name:  "ls/invalid-sort-errors",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--json", "--sort", "bogus"},
	},
	{
		// Legacy passes an unknown --type through both type blocks (neither runs) →
		// empty set → `null`. The server lsView matches; pinned as parity.
		name:  "ls/invalid-type-null",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--type", "bogus", "--json"},
	},
	{
		// Legacy excludes raw from ls's allowed output set → identical error on both.
		name:  "ls/output-raw-unsupported",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--output", "raw"},
	},
	{
		name:  "ls/malformed-cursor-errors",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--json", "--cursor", "not-a-valid-cursor"},
	},

	// find via wrkq.task.findListView (server compat list projection: recursive
	// path-prefix matching, metadata filters, cursor.Apply + limit+1 +
	// sort-validation + BuildNextCursor over the filtered set, mixed-type
	// in-memory merge-sort). findMixed seeds nested containers + tasks in varied
	// states/kinds so filters and recursion are all exercised.
	{
		name:  "find/all-default",
		setup: findMixed,
		args:  []string{"find", "--ndjson"},
	},
	{
		name:  "find/path-prefix-recursive",
		setup: findMixed,
		args:  []string{"find", "proj", "--ndjson"},
	},
	{
		name:  "find/type-t",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--ndjson"},
	},
	{
		name:  "find/type-p",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "p", "--ndjson"},
	},
	{
		name:  "find/state-open",
		setup: findMixed,
		args:  []string{"find", "proj", "--state", "open", "--ndjson"},
	},
	{
		name:  "find/state-all",
		setup: findMixed,
		args:  []string{"find", "proj", "--state", "all", "--ndjson"},
	},
	{
		name:  "find/kind-bug",
		setup: findMixed,
		args:  []string{"find", "proj", "--kind", "bug", "--ndjson"},
	},
	{
		name:  "find/slug-glob",
		setup: findMixed,
		args:  []string{"find", "proj", "--slug-glob", "task-*", "--ndjson"},
	},
	{
		name:  "find/json-pretty",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--json"},
	},
	{
		name:  "find/sort-id",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--sort", "id", "--ndjson"},
	},
	{
		name:  "find/sort-created_at",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--sort", "created_at", "--ndjson"},
	},
	{
		name:  "find/sort-updated_at",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--sort", "updated_at", "--ndjson"},
	},
	{
		name:  "find/sort-path",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--sort", "path", "--ndjson"},
	},
	{
		name:  "find/reverse",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--sort", "id", "--reverse", "--ndjson"},
	},
	{
		name:  "find/porcelain-limit-cursor",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--porcelain", "--limit", "2"},
	},
	{
		// Mixed-type (no --type) ignores the cursor entirely (legacy searchBoth
		// path skips pagination); proves the mirror replicates that exactly.
		name:  "find/mixed-cursor-ignored",
		setup: findMixed,
		args:  []string{"find", "proj", "--ndjson", "--cursor", "not-a-valid-cursor"},
	},
	{
		// Single-type cursor IS applied SQL-side → malformed cursor errors.
		name:  "find/type-t-malformed-cursor-errors",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--ndjson", "--cursor", "not-a-valid-cursor"},
	},
	{
		// find does NOT resolve search paths; an unknown path is a no-match filter,
		// NOT an error → empty result (json `[]`, ndjson nothing).
		name:  "find/unknown-path-empty-json",
		setup: findMixed,
		args:  []string{"find", "nope", "--json"},
	},
	{
		name:  "find/empty-json",
		setup: [][]string{{"mkdir", "void"}},
		args:  []string{"find", "void/nothing-here", "--json"},
	},
	{
		name:  "find/invalid-sort-errors",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--sort", "bogus", "--json"},
	},
	{
		// Unknown --parent-task is a resolution error surfaced raw by both binaries.
		name:  "find/unknown-parent-task-errors",
		setup: findMixed,
		args:  []string{"find", "--parent-task", "T-09999999", "--json"},
	},
	{
		// Legacy excludes raw from find's allowed output set → identical error.
		name:  "find/output-raw-unsupported",
		setup: findMixed,
		args:  []string{"find", "proj", "--output", "raw"},
	},

	// tree via wrkq.task.treeView (server-owned compat tree projection: recursive
	// traversal, container pruning, "all done" rollups, subtask nesting, hidden
	// counting). Only deterministic modes are parity-tested; the pretty/human
	// renderer is TTY-only + non-deterministic ("opened N ago") and is hard-gated.
	{
		name:  "tree/top-level-ndjson-default",
		setup: treeMixed,
		args:  []string{"tree"},
	},
	{
		name:  "tree/top-level-json",
		setup: treeMixed,
		args:  []string{"tree", "--json"},
	},
	{
		name:  "tree/subtree-json",
		setup: treeMixed,
		args:  []string{"tree", "proj", "--json"},
	},
	{
		name:  "tree/subtree-ndjson",
		setup: treeMixed,
		args:  []string{"tree", "proj", "--ndjson"},
	},
	{
		name:  "tree/subtree-porcelain",
		setup: treeMixed,
		args:  []string{"tree", "proj", "--porcelain"},
	},
	{
		name:  "tree/depth-limit",
		setup: treeMixed,
		args:  []string{"tree", "proj", "-L", "1", "--json"},
	},
	{
		name:  "tree/open-only",
		setup: treeMixedWithStates,
		args:  []string{"tree", "proj", "--open", "--json"},
	},
	{
		name:  "tree/all-includes-completed-and-empty",
		setup: treeMixedWithStates,
		args:  []string{"tree", "proj", "--all", "--json"},
	},
	{
		name:  "tree/default-hides-completed",
		setup: treeMixedWithStates,
		args:  []string{"tree", "proj", "--json"},
	},
	{
		name:  "tree/all-done-collapses",
		setup: treeAllDone,
		args:  []string{"tree", "proj", "--json"},
	},
	{
		name:  "tree/nested-subtasks",
		setup: treeSubtasks,
		args:  []string{"tree", "proj", "--json"},
	},
	{
		name:  "tree/nested-subtasks-ndjson",
		setup: treeSubtasks,
		args:  []string{"tree", "proj", "--ndjson"},
	},
	{
		name:  "tree/empty-container",
		setup: [][]string{{"mkdir", "empty"}},
		args:  []string{"tree", "empty", "--json"},
	},
	{
		name:  "tree/unknown-path-errors",
		setup: nil,
		args:  []string{"tree", "nope", "--json"},
	},
	{
		// raw is excluded from tree's allowed output set → identical error both sides.
		name:  "tree/output-raw-unsupported",
		setup: treeMixed,
		args:  []string{"tree", "proj", "--output", "raw"},
	},

	// check blocked ✓ RPC-backed via wrkq.task.blockedView (server compat projection
	// over store.Tasks.BlockedBy). Non-TTY → indented JSON; blocked → exit 1 with the
	// JSON body on stdout AND an Error line on stderr.
	{
		name:  "check-blocked/not-blocked",
		setup: [][]string{{"touch", "inbox/free", "-t", "Free"}},
		args:  []string{"check", "blocked", "inbox/free"},
	},
	{
		name: "check-blocked/blocked",
		setup: [][]string{
			{"touch", "inbox/main", "-t", "Main"},
			{"touch", "inbox/blk", "-t", "Blocker"},
			{"relation", "add", "inbox/blk", "blocks", "inbox/main"},
		},
		args: []string{"check", "blocked", "inbox/main"},
	},
	{
		name: "check-blocked/blocked-completed-not-counted",
		setup: [][]string{
			{"touch", "inbox/main2", "-t", "Main2"},
			{"touch", "inbox/blk2", "-t", "Blocker2"},
			{"relation", "add", "inbox/blk2", "blocks", "inbox/main2"},
			{"set", "inbox/blk2", "--state", "completed"},
		},
		args: []string{"check", "blocked", "inbox/main2"},
	},
	{
		name: "check-blocked/quiet-not-blocked",
		setup: [][]string{
			{"touch", "inbox/qfree", "-t", "QFree"},
		},
		args: []string{"check", "blocked", "inbox/qfree", "--quiet"},
	},
	{
		name: "check-blocked/quiet-blocked",
		setup: [][]string{
			{"touch", "inbox/qmain", "-t", "QMain"},
			{"touch", "inbox/qblk", "-t", "QBlocker"},
			{"relation", "add", "inbox/qblk", "blocks", "inbox/qmain"},
		},
		args: []string{"check", "blocked", "inbox/qmain", "--quiet"},
	},
	{
		name:  "check-blocked/unknown-ref-errors",
		setup: nil,
		args:  []string{"check", "blocked", "T-09999999"},
	},

	// check-inbox ✓ RPC-backed via wrkq.task.inboxView (server compat list projection:
	// open inbox tasks + ack-pending requested-by tasks). Non-TTY default → ndjson.
	{
		name:  "check-inbox/empty",
		setup: nil,
		args:  []string{"check-inbox"},
	},
	{
		name: "check-inbox/open-tasks-ndjson",
		setup: [][]string{
			{"touch", "inbox/one", "-t", "One", "--priority", "2"},
			{"touch", "inbox/two", "-t", "Two", "--priority", "1"},
		},
		args: []string{"check-inbox"},
	},
	{
		name: "check-inbox/json",
		setup: [][]string{
			{"touch", "inbox/one", "-t", "One"},
		},
		args: []string{"check-inbox", "--json"},
	},
	{
		name: "check-inbox/excludes-non-open",
		setup: [][]string{
			{"touch", "inbox/openish", "-t", "Open"},
			{"touch", "inbox/donish", "-t", "Done"},
			{"set", "inbox/donish", "--state", "completed"},
		},
		args: []string{"check-inbox"},
	},
	{
		// Project-root scoping: with a root configured, the inbox path is scoped
		// to <root>/inbox AND ack-pending tasks requested by <root> are surfaced.
		name: "check-inbox/project-root-scoped",
		setup: [][]string{
			{"mkdir", "myproj"},
			{"mkdir", "myproj/inbox"},
			{"touch", "myproj/inbox/scoped", "-t", "Scoped"},
		},
		args: []string{"check-inbox", "--json"},
		env:  []string{"WRKQ_PROJECT_ROOT=myproj"},
	},

	// ── UNGATED mirror-only modes now byte-proven against legacy ──
	// table/human render through the SAME internal/render.FormatTable path (legacy's
	// runLs switch has no human case → falls through to the table renderer), so both
	// produce identical bytes. yaml/tsv decode the compat projection back into the
	// legacy struct so render output is identical.
	{
		name:  "ls/output-table",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--output", "table"},
	},
	{
		name:  "ls/output-human",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--output", "human"},
	},
	{
		name:  "ls/output-yaml",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--output", "yaml"},
	},
	{
		name:  "ls/output-tsv",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--output", "tsv"},
	},
	{
		// Empty set table render → legacy RenderTable returns nil for 0 rows (no
		// header either). The mirror matches via the same render path.
		name:  "ls/output-table-empty",
		setup: [][]string{{"mkdir", "empty"}},
		args:  []string{"ls", "empty", "--output", "table"},
	},
	{
		// --one emits entry.Path per line (newline-joined + trailing newline).
		name:  "ls/one",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--one"},
	},
	{
		// --nul emits entry.Path NUL-separated with NO trailing delimiter.
		name:  "ls/nul",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--nul"},
	},
	{
		// --one over an empty set emits nothing.
		name:  "ls/one-empty",
		setup: [][]string{{"mkdir", "empty"}},
		args:  []string{"ls", "empty", "--one"},
	},
	{
		// --recursive/-R is a no-op in legacy (rollups already recurse) → identical
		// to the un-flagged listing. Accepted-and-ignored by the mirror.
		name:  "ls/recursive-noop",
		setup: lsMixed,
		args:  []string{"ls", "proj", "-R", "--json"},
	},
	{
		// Multi-path: the server queries each path and merge-sorts the COMBINED set.
		name:  "ls/multi-path",
		setup: lsMixed,
		args:  []string{"ls", "proj/alpha", "proj/beta", "--json"},
	},
	{
		// Multi-path with a limit exercises the combined limit+1 / next-cursor.
		name:  "ls/multi-path-limit-cursor",
		setup: lsMixed,
		args:  []string{"ls", "proj/alpha", "proj", "--porcelain", "--limit", "2"},
	},
	{
		// Multi-path where one path does not exist → legacy errors mid-iteration.
		name:  "ls/multi-path-unknown-errors",
		setup: lsMixed,
		args:  []string{"ls", "proj", "nope", "--json"},
	},
	{
		// --one over multiple paths emits the merged paths.
		name:  "ls/multi-path-one",
		setup: lsMixed,
		args:  []string{"ls", "proj/alpha", "proj/beta", "--one"},
	},
	{
		// Conflicting output modes error identically on both binaries.
		name:  "ls/conflicting-modes-errors",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--json", "--ndjson"},
	},

	// ── project-root scoping parity (WRKQ_PROJECT_ROOT / ASP_PROJECT / --project) ──
	// Both binaries apply the SAME neutral projectroot transform before any RPC
	// param is sent. The seed is root-less; only the command-under-test runs with a
	// project root. Proves ls + a selector read (cat/stat) + mutations (touch/set/mv)
	// per daedalus's invariant.
	{
		name:  "pr/ls-default-root",
		setup: prSeed,
		args:  []string{"ls", "--json"},
		env:   []string{"WRKQ_PROJECT_ROOT=myproj"},
	},
	{
		name:  "pr/ls-relative-path",
		setup: prSeed,
		args:  []string{"ls", "sub", "--json"},
		env:   []string{"WRKQ_PROJECT_ROOT=myproj"},
	},
	{
		// find scopes its raw search paths through the same neutral projectroot
		// transform; a bare `find` under a root searches the root recursively.
		name:  "pr/find-default-root",
		setup: prSeed,
		args:  []string{"find", "--type", "t", "--ndjson"},
		env:   []string{"WRKQ_PROJECT_ROOT=myproj"},
	},
	{
		name:  "pr/cat-relative-selector",
		setup: prSeed,
		args:  []string{"cat", "task-a", "--json"},
		env:   []string{"WRKQ_PROJECT_ROOT=myproj"},
	},
	{
		name:  "pr/stat-relative-selector",
		setup: prSeed,
		args:  []string{"stat", "task-a"},
		env:   []string{"WRKQ_PROJECT_ROOT=myproj"},
	},
	{
		name:          "pr/touch-relative-path",
		setup:         prSeed,
		args:          []string{"touch", "newtask", "-t", "New"},
		env:           []string{"WRKQ_PROJECT_ROOT=myproj"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		name:    "pr/set-relative-selector",
		setup:   prSeed,
		args:    []string{"set", "task-a", "--state", "in_progress"},
		env:     []string{"WRKQ_PROJECT_ROOT=myproj"},
		mutates: true,
	},
	{
		name:    "pr/mv-relative-selectors",
		setup:   prSeed,
		args:    []string{"mv", "task-b", "sub"},
		env:     []string{"WRKQ_PROJECT_ROOT=myproj"},
		mutates: true,
	},
	{
		name:  "pr/tree-default-root",
		setup: prSeed,
		args:  []string{"tree", "--json"},
		env:   []string{"WRKQ_PROJECT_ROOT=myproj"},
	},
	{
		name:  "pr/tree-relative-path",
		setup: prSeed,
		args:  []string{"tree", "sub", "--json"},
		env:   []string{"WRKQ_PROJECT_ROOT=myproj"},
	},
	{
		// ASP_PROJECT is the agent-runtime project hint; config.Load honors it as a
		// project-root source. Same effect as WRKQ_PROJECT_ROOT for this case.
		name:  "pr/asp-project-ls",
		setup: prSeed,
		args:  []string{"ls", "--json"},
		env:   []string{"ASP_PROJECT=myproj"},
	},
	{
		// --project OVERRIDES the configured root: env points at `other` (no task-a),
		// but --project myproj scopes the read into myproj. If the mirror ignored
		// --project it would look under `other` and diverge.
		name:  "pr/project-flag-override-ls",
		setup: prSeed,
		args:  []string{"--project", "myproj", "ls", "--json"},
		env:   []string{"WRKQ_PROJECT_ROOT=other"},
	},
	{
		name:  "pr/project-flag-override-cat",
		setup: prSeed,
		args:  []string{"--project", "myproj", "cat", "task-a", "--json"},
		env:   []string{"WRKQ_PROJECT_ROOT=other"},
	},
}

// prSeed builds a root-less fixture with two top-level projects (myproj, other),
// a sub-container, and tasks under myproj. The project-root parity cases run the
// command-under-test WITH a project root so the mirror must scope relative
// paths/selectors into myproj exactly as legacy does.
var prSeed = [][]string{
	{"mkdir", "myproj"},
	{"mkdir", "myproj/sub"},
	{"mkdir", "other"},
	{"touch", "myproj/task-a", "-t", "Task A"},
	{"touch", "myproj/task-b", "-t", "Task B"},
}

// lsMixed seeds a project container with child containers + tasks so ls exercises
// mixed task/container listing, rollup counts (alpha has a nested descendant
// task), and merge-sort ordering across the combined set.
var lsMixed = [][]string{
	{"mkdir", "proj"},
	{"mkdir", "proj/alpha"},
	{"mkdir", "proj/beta"},
	{"touch", "proj/alpha/nested", "-t", "Nested"},
	{"touch", "proj/task-x", "-t", "Task X"},
	{"touch", "proj/task-y", "-t", "Task Y"},
}

// findMixed seeds nested containers plus tasks in varied states/kinds so the
// find parity cases exercise recursive path-prefix matching, per-filter
// narrowing (state/type/kind/slug-glob), and mixed-type merge-sort. Tasks default
// to state "open" (find's default excludes archived/deleted/idea, so open tasks
// are visible); one is flipped to completed and one to a bug kind.
var findMixed = [][]string{
	{"mkdir", "proj"},
	{"mkdir", "proj/sub"},
	{"touch", "proj/task-a", "-t", "Task A"},
	{"touch", "proj/task-b", "-t", "Task B", "--kind", "bug"},
	{"touch", "proj/sub/task-c", "-t", "Task C"},
	{"set", "proj/task-a", "--state", "open"},
	{"set", "proj/task-b", "--state", "open"},
	{"set", "proj/sub/task-c", "--state", "completed"},
}

// treeMixed seeds a project with child containers (one nested deeper) + tasks so
// tree exercises recursive traversal, pruning of empty containers, and mixed
// task/container rendering. alpha has a nested descendant task; beta is empty
// (pruned by default).
var treeMixed = [][]string{
	{"mkdir", "proj"},
	{"mkdir", "proj/alpha"},
	{"mkdir", "proj/beta"},
	{"touch", "proj/alpha/nested", "-t", "Nested"},
	{"touch", "proj/task-x", "-t", "Task X"},
	{"touch", "proj/task-y", "-t", "Task Y"},
}

// treeMixedWithStates seeds tasks in distinct states so --all / --open /
// default-hide filtering and the "all done" rollup are exercised distinctly:
// an open task, a draft task, and a completed task.
var treeMixedWithStates = [][]string{
	{"mkdir", "proj"},
	{"mkdir", "proj/alpha"},
	{"touch", "proj/alpha/nested", "-t", "Nested"},
	{"touch", "proj/task-open", "-t", "Open Task"},
	{"touch", "proj/task-draft", "-t", "Draft Task", "--state", "draft"},
	{"touch", "proj/task-done", "-t", "Done Task"},
	{"set", "proj/task-done", "--state", "completed"},
}

// treeAllDone seeds a project whose only task is completed so the container
// collapses to "(All done)" and the task is omitted from the default tree.
var treeAllDone = [][]string{
	{"mkdir", "proj"},
	{"touch", "proj/done", "-t", "Done"},
	{"set", "proj/done", "--state", "completed"},
}

// treeSubtasks seeds a parent task with a child subtask so the tree exercises
// in-set subtask nesting (the child nests under its parent node).
var treeSubtasks = [][]string{
	{"mkdir", "proj"},
	{"touch", "proj/parent", "-t", "Parent"},
	{"touch", "proj/child", "-t", "Child", "--parent-task", "proj/parent"},
}

func TestParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds wrkq + wrkq-rpccli and runs both CLIs")
	}
	bins := buildParityBinaries(t)
	for _, tc := range parityCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Seed ONE base fixture, then give each binary a byte-identical copy
			// (incl. random UUIDs), so output comparison catches real id bugs
			// rather than masking legitimately-different independent seeds.
			base := seedFixture(t, bins, tc.setup)
			oldDir := copyFixture(t, base)
			newDir := copyFixture(t, base)

			oldRes := runCLIEnv(t, bins.wrkq, oldDir, tc.args, tc.env)
			newRes := runCLIEnv(t, bins.mirror, newDir, tc.args, tc.env)

			if oldRes.exit != newRes.exit {
				t.Errorf("exit code: old=%d new=%d\n old stderr: %s\n new stderr: %s",
					oldRes.exit, newRes.exit, oldRes.stderr, newRes.stderr)
			}
			norm := func(s string) string {
				s = normalize(s)
				if tc.normalizeUUID {
					s = uuidRe.ReplaceAllString(s, "<UUID>")
				}
				return s
			}
			if got, want := norm(newRes.stdout), norm(oldRes.stdout); got != want {
				t.Errorf("stdout mismatch:\n old: %q\n new: %q", want, got)
			}
			if got, want := norm(newRes.stderr), norm(oldRes.stderr); got != want {
				t.Errorf("stderr mismatch:\n old: %q\n new: %q", want, got)
			}
			if tc.mutates {
				if got, want := snapshot(t, newDir), snapshot(t, oldDir); got != want {
					t.Errorf("durable task snapshot mismatch:\n old:\n%s\n new:\n%s", want, got)
				}
			}
		})
	}
}

// Ã¢ÂÂÃ¢ÂÂ harness internals Ã¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂÃ¢ÂÂ

// TestCommentLsCursorReplay proves the paginated list-view cursor contract beyond
// first-page emission: cursor replay across page boundaries, per-page byte parity
// between old and new, and no-dup/no-miss (concatenated pages equal unpaginated).
func TestCommentLsCursorReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs")
	}
	bins := buildParityBinaries(t)
	setup := [][]string{{"touch", "inbox/page", "-t", "P"}}
	for _, b := range []string{"c1", "c2", "c3", "c4", "c5"} {
		setup = append(setup, []string{"comment", "add", "inbox/page", b})
	}
	base := seedFixture(t, bins, setup)
	oldDir := copyFixture(t, base)
	newDir := copyFixture(t, base)

	paginateAll := func(bin, dir string) (all, pages []string) {
		cur := ""
		for {
			args := []string{"comment", "ls", "inbox/page", "--porcelain", "--limit", "2"}
			if cur != "" {
				args = append(args, "--cursor", cur)
			}
			res := runCLI(t, bin, dir, args)
			if res.exit != 0 {
				t.Fatalf("%s page (cursor=%q) exit %d: %s", bin, cur, res.exit, res.stderr)
			}
			all = append(all, nonEmptyLines(res.stdout)...)
			pages = append(pages, res.stdout+"\x1e"+res.stderr)
			next := extractCursor(res.stderr)
			if next == "" {
				break
			}
			cur = next
			if len(pages) > 20 {
				t.Fatal("pagination did not terminate")
			}
		}
		return all, pages
	}

	oldAll, oldPages := paginateAll(bins.wrkq, oldDir)
	newAll, newPages := paginateAll(bins.mirror, newDir)

	if len(oldPages) != len(newPages) {
		t.Fatalf("page count: old=%d new=%d", len(oldPages), len(newPages))
	}
	for i := range oldPages {
		if oldPages[i] != newPages[i] {
			t.Errorf("page %d bytes differ:\n old: %q\n new: %q", i, oldPages[i], newPages[i])
		}
	}

	oldFull := nonEmptyLines(runCLI(t, bins.wrkq, oldDir, []string{"comment", "ls", "inbox/page"}).stdout)
	newFull := nonEmptyLines(runCLI(t, bins.mirror, newDir, []string{"comment", "ls", "inbox/page"}).stdout)
	if strings.Join(oldAll, "\n") != strings.Join(oldFull, "\n") {
		t.Errorf("OLD paginated != unpaginated (dup/miss):\n paged: %v\n full: %v", oldAll, oldFull)
	}
	if strings.Join(newAll, "\n") != strings.Join(newFull, "\n") {
		t.Errorf("NEW paginated != unpaginated (dup/miss):\n paged: %v\n full: %v", newAll, newFull)
	}
	if len(oldAll) != 5 || len(newAll) != 5 {
		t.Errorf("expected 5 rows across pages: old=%d new=%d", len(oldAll), len(newAll))
	}
}

// TestLsCursorReplay proves the ls list-view cursor contract over the MIXED
// task/container set: cursor replay across page boundaries, per-page byte parity
// old-vs-new, and no-dup/no-miss (concatenated pages equal the unpaginated list).
// daedalus requires this distinct from the first-page parity case for ls/find.
func TestLsCursorReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs")
	}
	bins := buildParityBinaries(t)
	// 5 mixed entries under proj: containers alpha,beta + tasks task-1,task-2,task-3.
	// Sorted by slug: alpha, beta, task-1, task-2, task-3 → pages of 2,2,1.
	setup := [][]string{{"mkdir", "proj"}, {"mkdir", "proj/alpha"}, {"mkdir", "proj/beta"}}
	for _, s := range []string{"task-1", "task-2", "task-3"} {
		setup = append(setup, []string{"touch", "proj/" + s, "-t", s})
	}
	base := seedFixture(t, bins, setup)
	oldDir := copyFixture(t, base)
	newDir := copyFixture(t, base)

	paginateAll := func(bin, dir string) (all, pages []string) {
		cur := ""
		for {
			args := []string{"ls", "proj", "--porcelain", "--limit", "2"}
			if cur != "" {
				args = append(args, "--cursor", cur)
			}
			res := runCLI(t, bin, dir, args)
			if res.exit != 0 {
				t.Fatalf("%s page (cursor=%q) exit %d: %s", bin, cur, res.exit, res.stderr)
			}
			all = append(all, nonEmptyLines(res.stdout)...)
			pages = append(pages, res.stdout+"\x1e"+res.stderr)
			next := extractCursor(res.stderr)
			if next == "" {
				break
			}
			cur = next
			if len(pages) > 20 {
				t.Fatal("pagination did not terminate")
			}
		}
		return all, pages
	}

	oldAll, oldPages := paginateAll(bins.wrkq, oldDir)
	newAll, newPages := paginateAll(bins.mirror, newDir)

	if len(oldPages) != len(newPages) {
		t.Fatalf("page count: old=%d new=%d", len(oldPages), len(newPages))
	}
	for i := range oldPages {
		if oldPages[i] != newPages[i] {
			t.Errorf("page %d bytes differ:\n old: %q\n new: %q", i, oldPages[i], newPages[i])
		}
	}

	oldFull := nonEmptyLines(runCLI(t, bins.wrkq, oldDir, []string{"ls", "proj"}).stdout)
	newFull := nonEmptyLines(runCLI(t, bins.mirror, newDir, []string{"ls", "proj"}).stdout)
	if strings.Join(oldAll, "\n") != strings.Join(oldFull, "\n") {
		t.Errorf("OLD paginated != unpaginated (dup/miss):\n paged: %v\n full: %v", oldAll, oldFull)
	}
	if strings.Join(newAll, "\n") != strings.Join(newFull, "\n") {
		t.Errorf("NEW paginated != unpaginated (dup/miss):\n paged: %v\n full: %v", newAll, newFull)
	}
	if len(oldAll) != 5 || len(newAll) != 5 {
		t.Errorf("expected 5 rows across pages: old=%d new=%d", len(oldAll), len(newAll))
	}
}

// TestFindCursorReplay proves the find list-view cursor contract over the
// FILTERED/RECURSIVE single-type set: cursor replay across page boundaries,
// per-page byte parity old-vs-new, and no-dup/no-miss (concatenated pages equal
// the unpaginated list). daedalus REQUIRES this distinct from the first-page
// parity case. Single-type (--type t) is used because the mixed (searchBoth) path
// deliberately ignores the cursor (legacy parity, covered in find/mixed-cursor-*).
func TestFindCursorReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs")
	}
	bins := buildParityBinaries(t)
	// 5 open tasks across nested containers under proj; find recurses the prefix.
	// Sorted by id ASC → stable pages of 2,2,1.
	setup := [][]string{{"mkdir", "proj"}, {"mkdir", "proj/sub"}}
	for _, s := range []string{"t1", "t2", "t3"} {
		setup = append(setup, []string{"touch", "proj/" + s, "-t", s})
	}
	for _, s := range []string{"t4", "t5"} {
		setup = append(setup, []string{"touch", "proj/sub/" + s, "-t", s})
	}
	base := seedFixture(t, bins, setup)
	oldDir := copyFixture(t, base)
	newDir := copyFixture(t, base)

	paginateAll := func(bin, dir string) (all, pages []string) {
		cur := ""
		for {
			args := []string{"find", "proj", "--type", "t", "--sort", "id", "--porcelain", "--limit", "2"}
			if cur != "" {
				args = append(args, "--cursor", cur)
			}
			res := runCLI(t, bin, dir, args)
			if res.exit != 0 {
				t.Fatalf("%s page (cursor=%q) exit %d: %s", bin, cur, res.exit, res.stderr)
			}
			all = append(all, nonEmptyLines(res.stdout)...)
			pages = append(pages, res.stdout+"\x1e"+res.stderr)
			next := extractCursor(res.stderr)
			if next == "" {
				break
			}
			cur = next
			if len(pages) > 20 {
				t.Fatal("pagination did not terminate")
			}
		}
		return all, pages
	}

	oldAll, oldPages := paginateAll(bins.wrkq, oldDir)
	newAll, newPages := paginateAll(bins.mirror, newDir)

	if len(oldPages) != len(newPages) {
		t.Fatalf("page count: old=%d new=%d", len(oldPages), len(newPages))
	}
	for i := range oldPages {
		if oldPages[i] != newPages[i] {
			t.Errorf("page %d bytes differ:\n old: %q\n new: %q", i, oldPages[i], newPages[i])
		}
	}

	oldFull := nonEmptyLines(runCLI(t, bins.wrkq, oldDir, []string{"find", "proj", "--type", "t", "--sort", "id", "--ndjson"}).stdout)
	newFull := nonEmptyLines(runCLI(t, bins.mirror, newDir, []string{"find", "proj", "--type", "t", "--sort", "id", "--ndjson"}).stdout)
	if strings.Join(oldAll, "\n") != strings.Join(oldFull, "\n") {
		t.Errorf("OLD paginated != unpaginated (dup/miss):\n paged: %v\n full: %v", oldAll, oldFull)
	}
	if strings.Join(newAll, "\n") != strings.Join(newFull, "\n") {
		t.Errorf("NEW paginated != unpaginated (dup/miss):\n paged: %v\n full: %v", newAll, newFull)
	}
	if len(oldAll) != 5 || len(newAll) != 5 {
		t.Errorf("expected 5 rows across pages: old=%d new=%d", len(oldAll), len(newAll))
	}
}

// TestFindHardGates proves the mirror REFUSES (clean non-zero exit) the legacy
// find surface it does not yet implement, so a narrower behavior can never
// masquerade as parity. Legacy would succeed for these, so they cannot be
// byte-parity cases; the gate is asserted on the mirror alone.
func TestFindHardGates(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs the mirror CLI")
	}
	bins := buildParityBinaries(t)
	base := seedFixture(t, bins, findMixed)
	dir := copyFixture(t, base)

	gated := []struct {
		name string
		args []string
	}{
		{"print0", []string{"find", "proj", "--print0"}},
		{"output-yaml", []string{"find", "proj", "--output", "yaml"}},
		{"output-tsv", []string{"find", "proj", "--output", "tsv"}},
		{"output-table", []string{"find", "proj", "--output", "table"}},
		{"output-human", []string{"find", "proj", "--output", "human"}},
		{"conflicting-modes", []string{"find", "proj", "--json", "--ndjson"}},
	}
	for _, g := range gated {
		g := g
		t.Run(g.name, func(t *testing.T) {
			res := runCLI(t, bins.mirror, dir, g.args)
			if res.exit == 0 {
				t.Errorf("expected non-zero exit (hard-gate) for %v, got 0\n stdout: %q", g.args, res.stdout)
			}
			if strings.TrimSpace(res.stderr) == "" {
				t.Errorf("expected a gate error message on stderr for %v", g.args)
			}
		})
	}
}

// TestLsHardGates previously proved the mirror REFUSED the legacy ls surfaces it
// did not yet implement. All of those (table/human/yaml/tsv, --one/--nul,
// --recursive, multi-path, conflicting-modes) now have REAL byte parity and live
// in TestParity (ls/output-*, ls/one, ls/nul, ls/recursive-noop, ls/multi-path*,
// ls/conflicting-modes-errors). No ls surface remains hard-gated, so the gate test
// is retired — its coverage moved into the equivalence harness.

// TestTreeHardGates proves the mirror REFUSES (clean non-zero exit, not silent
// degradation) every legacy tree surface it does not yet implement, so a narrower
// behavior can never masquerade as parity. Legacy would succeed for these, so they
// cannot be byte-parity cases; we assert the gate on the mirror alone. The
// pretty/human renderer is the load-bearing gate: it is TTY-only and embeds
// wall-clock-relative "opened N ago" strings, so it cannot be byte-reproduced.
func TestTreeHardGates(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs the mirror CLI")
	}
	bins := buildParityBinaries(t)
	base := seedFixture(t, bins, treeMixed)
	dir := copyFixture(t, base)

	gated := []struct {
		name string
		args []string
	}{
		{"output-human", []string{"tree", "proj", "--output", "human"}},
		{"output-table", []string{"tree", "proj", "--output", "table"}},
		{"output-yaml", []string{"tree", "proj", "--output", "yaml"}},
		{"output-tsv", []string{"tree", "proj", "--output", "tsv"}},
		{"multi-path", []string{"tree", "proj", "inbox", "--json"}},
		{"fields", []string{"tree", "proj", "--json", "--fields", "id,slug"}},
		{"conflicting-modes", []string{"tree", "proj", "--json", "--ndjson"}},
	}
	for _, g := range gated {
		g := g
		t.Run(g.name, func(t *testing.T) {
			res := runCLI(t, bins.mirror, dir, g.args)
			if res.exit == 0 {
				t.Errorf("expected non-zero exit (hard-gate) for %v, got 0\n stdout: %q", g.args, res.stdout)
			}
			if strings.TrimSpace(res.stderr) == "" {
				t.Errorf("expected a gate error message on stderr for %v", g.args)
			}
		})
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func extractCursor(stderr string) string {
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, "next_cursor=") {
			return strings.TrimPrefix(l, "next_cursor=")
		}
	}
	return ""
}

type binaries struct{ wrkq, mirror, wrkqadm string }

var (
	parityBins  binaries
	parityOnce  sync.Once
	parityBuilt bool
	// sharedHome is a single HOME used for every binary invocation so HOME-derived
	// values (e.g. cat's artifact_dir = $HOME/praesidium/var/wrkq-artifacts/<id>)
	// are identical for old and new, which run in separate fixture dirs. The DB is
	// always located via --db, so HOME need not be the fixture dir.
	sharedHome string
)

func buildParityBinaries(t *testing.T) binaries {
	t.Helper()
	parityOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rpccli-parity-bins")
		if err != nil {
			t.Fatalf("mkdtemp: %v", err)
		}
		sharedHome = filepath.Join(dir, "home")
		if err := os.MkdirAll(sharedHome, 0o755); err != nil {
			t.Fatalf("mkdir sharedHome: %v", err)
		}
		root := repoRootFromTest(t)
		build := func(name, pkg string) string {
			out := filepath.Join(dir, name)
			cmd := exec.Command("go", "build", "-tags", "sqlite_fts5", "-o", out, pkg)
			cmd.Dir = root
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build %s: %v\n%s", pkg, err, b)
			}
			return out
		}
		parityBins = binaries{
			wrkq:    build("wrkq", "./cmd/wrkq"),
			mirror:  build("wrkq-rpccli", "./cmd/wrkq-rpccli"),
			wrkqadm: build("wrkqadm", "./cmd/wrkqadm"),
		}
		parityBuilt = true
	})
	if !parityBuilt {
		t.Fatal("parity binaries failed to build")
	}
	return parityBins
}

// seedFixture creates a hermetic fixture dir (own HOME, no inherited project
// scope, cwd inside the dir so config.Load does not walk into the repo's
// .env.local), runs `wrkqadm init`, and applies the setup commands with the
// legacy binary so both fixtures start identical.
func seedFixture(t *testing.T, bins binaries, setup [][]string) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wrkq.db")
	mustRun(t, bins.wrkqadm, dir, []string{"--db", dbPath, "init"})
	for _, argv := range setup {
		mustRun(t, bins.wrkq, dir, append([]string{"--db", dbPath, "--as", "local-human"}, argv...))
	}
	return dir
}

// copyFixture copies a seeded fixture's SQLite files (main + WAL + shm) into a
// fresh dir so both binaries run against byte-identical starting state. The
// seeding processes have exited and committed, so the triplet copy is consistent.
func copyFixture(t *testing.T, base string) string {
	t.Helper()
	dst := t.TempDir()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(filepath.Join(base, "wrkq.db"+suffix))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("copy fixture read: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dst, "wrkq.db"+suffix), b, 0o644); err != nil {
			t.Fatalf("copy fixture write: %v", err)
		}
	}
	return dst
}

type cliResult struct {
	exit   int
	stdout string
	stderr string
}

func runCLI(t *testing.T, bin, dir string, args []string) cliResult {
	return runCLIEnv(t, bin, dir, args, nil)
}

func runCLIEnv(t *testing.T, bin, dir string, args []string, extraEnv []string) cliResult {
	t.Helper()
	full := append([]string{"--db", filepath.Join(dir, "wrkq.db"), "--as", "local-human"}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Dir = dir
	cmd.Env = append(hermeticEnv(), extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			t.Fatalf("run %s: %v", bin, err)
		}
	}
	return cliResult{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}

func mustRun(t *testing.T, bin, dir string, args []string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = hermeticEnv()
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed %s %v: %v\n%s", bin, args, err, b)
	}
}

func hermeticEnv() []string {
	return []string{"HOME=" + sharedHome, "PATH=" + os.Getenv("PATH")}
}

var rfc3339Re = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)
var uuidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// normalize neutralizes wall-clock timestamps so byte comparison is stable for
// commands whose output embeds them (ack has none; this future-proofs the harness).
func normalize(s string) string {
	return rfc3339Re.ReplaceAllString(s, "<TS>")
}

// snapshot renders the durable task state as a stable string: id, slug, state,
// priority, kind, etag, and whether acknowledged/completed timestamps are set
// (presence, not value ✓ the wall-clock value legitimately differs per run).
// Provenance columns (updated_by, via) are intentionally excluded: the RPC path
// records via='rpc' by design, which is a correct difference, not a regression.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	database, err := db.Open(filepath.Join(dir, "wrkq.db"))
	if err != nil {
		t.Fatalf("snapshot open: %v", err)
	}
	defer func() { _ = database.Close() }()
	rows, err := database.Query(`
		SELECT id, slug, state, priority, kind, etag,
		       CASE WHEN acknowledged_at IS NOT NULL AND acknowledged_at != '' THEN 'ack' ELSE '-' END,
		       CASE WHEN completed_at    IS NOT NULL AND completed_at    != '' THEN 'done' ELSE '-' END
		FROM tasks ORDER BY id`)
	if err != nil {
		t.Fatalf("snapshot query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var id, slug, state, kind, ackd, done string
		var prio, etag int
		if err := rows.Scan(&id, &slug, &state, &prio, &kind, &etag, &ackd, &done); err != nil {
			t.Fatalf("snapshot scan: %v", err)
		}
		b.WriteString("task|" + strings.Join([]string{id, slug, state, strconv.Itoa(prio), kind, strconv.Itoa(etag), ackd, done}, "|"))
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshot rows: %v", err)
	}

	crows, err := database.Query(`SELECT id, slug, kind FROM containers ORDER BY id`)
	if err != nil {
		t.Fatalf("snapshot container query: %v", err)
	}
	defer func() { _ = crows.Close() }()
	for crows.Next() {
		var id, slug, kind string
		if err := crows.Scan(&id, &slug, &kind); err != nil {
			t.Fatalf("snapshot container scan: %v", err)
		}
		b.WriteString("container|" + strings.Join([]string{id, slug, kind}, "|") + "\n")
	}
	if err := crows.Err(); err != nil {
		t.Fatalf("snapshot container rows: %v", err)
	}
	return b.String()
}
