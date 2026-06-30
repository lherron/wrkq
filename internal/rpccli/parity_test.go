package rpccli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
)

// TestParity is the data-driven old-vs-new equivalence harness. It is the single
// proof that the RPC-backed CLI is functionally equivalent to the legacy oracle:
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
	// normalizeRunDir neutralizes the old/new copied fixture directories in rendered
	// output. Use only for commands whose legitimate output includes the configured
	// database path (e.g. whoami).
	normalizeRunDir bool
	// env adds extra environment variables (beyond the hermetic HOME/PATH) for the
	// command-under-test run on BOTH binaries — e.g. WRKQ_PROJECT_ROOT/ASP_PROJECT
	// to prove project-root scoping. The setup/seed always runs root-less.
	env []string
	// files writes literal files into EACH binary's run dir (keyed by a relative
	// path) before the command-under-test runs, so commands that consume a host
	// file path (e.g. `attach put <task> <file>`) have a byte-identical source on
	// both sides. copyFixture only carries the SQLite triplet, so file inputs that
	// must exist at run time are seeded here instead.
	files map[string]string
	// stdin feeds the command-under-test's standard input on BOTH binaries (used by
	// `attach put <task> -`, which reads the attachment bytes from stdin). Raw
	// bytes — NUL/newlines survive the round-trip and are compared byte-for-byte.
	stdin []byte
	// seededAttachStore makes the harness seed the fixture under a SHARED ABSOLUTE
	// attach dir (<base>/attach-store) and inject WRKQ_ATTACH_DIR=<that abs> into the
	// command-under-test env for BOTH binaries. Required by `attach get`, whose
	// bytes must be reachable at get time from the SAME dir the seed `attach put`
	// wrote them into (the mirror's RPC server reads from the EXPLICITLY-configured
	// attach dir, never the HOME auto-default).
	seededAttachStore bool
	// seedEnv adds extra environment variables to the SETUP/seed run only (not the
	// command-under-test). Handoff seeding (`wrkq handoff create` in setup) needs an
	// agent/project scope env (ASP_SCOPE_REF) since handoff scope is caller-owned;
	// the seed always uses the LEGACY wrkq binary so both fixtures are byte-identical.
	seedEnv []string
	// searchIndex enables a DETERMINISTIC, hermetic search host for the case: the
	// `none` dense provider (pure lexical/FTS — no llama, no non-determinism) is set
	// on BOTH the seed and the command-under-test runs. When searchSeedRebuild is
	// also set, the harness seeds a SHARED ABSOLUTE sidecar (<base>/search.sqlite via
	// WRKQ_SEARCH_DB_PATH) and runs `index rebuild` against it at seed time, so a
	// read-only `search` command-under-test (which never rebuilds) finds the indexed
	// rows on both binaries from the SAME prebuilt sidecar. Without searchSeedRebuild
	// each binary opens a fresh per-dir sidecar (empty/stale) — deterministic for
	// status / empty-search / lifecycle cases.
	searchIndex       bool
	searchSeedRebuild bool
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

	// version ✓ local-only mirror command. Non-TTY default is indented JSON.
	{
		name: "version/default-json",
		args: []string{"version"},
	},
	{
		name: "version/explicit-json",
		args: []string{"version", "--json"},
	},
	// usage/info + agent-info ✓ local-only embedded documentation renderers.
	// Non-TTY default is indented JSON; TTY raw markdown is covered by installed smoke.
	{
		name: "usage/default-json",
		args: []string{"usage"},
	},
	{
		name: "usage/explicit-json",
		args: []string{"usage", "--json"},
	},
	{
		name: "usage/info-alias-json",
		args: []string{"info", "--json"},
	},
	{
		name: "agent-info/default-json",
		args: []string{"agent-info"},
	},
	{
		name: "agent-info/explicit-json",
		args: []string{"agent-info", "--json"},
	},
	// whoami ✓ local/config attribution parity. It resolves --as/env/scope/default
	// with the shared attribution package and emits the configured DB path.
	{
		name:            "whoami/default-json",
		args:            []string{"whoami"},
		normalizeRunDir: true,
	},
	{
		name:            "whoami/explicit-json",
		args:            []string{"whoami", "--json"},
		normalizeRunDir: true,
	},
	{
		name:            "whoami/scope-ref-with-explicit-principal",
		args:            []string{"whoami", "--json"},
		env:             []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		normalizeRunDir: true,
	},
	// agent-context ✓ local scope resolution with best-effort RPC-backed identity
	// lookups. It must also preserve the legacy unresolved-scope exit-2 contract.
	{
		name:            "agent-context/default-json-with-lookups",
		args:            []string{"agent-context"},
		env:             []string{"ASP_SCOPE_REF=agent:local-human:project:inbox"},
		normalizeRunDir: true,
	},
	{
		name:            "agent-context/explicit-json-override",
		args:            []string{"agent-context", "--scope", "local-human@inbox", "--json"},
		env:             []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		normalizeRunDir: true,
	},
	{
		name:            "agent-context/porcelain-compact",
		args:            []string{"agent-context", "--scope", "local-human@inbox", "--output", "porcelain"},
		normalizeRunDir: true,
	},
	{
		name:            "agent-context/unresolved-json-exit2",
		args:            []string{"agent-context", "--json"},
		normalizeRunDir: true,
	},
	// rpc --stdio ✓ protocol-serving command. The real stdio loop is covered by
	// TestTransportEquivalence_LegacyVsMirrorRPCStdio; this row covers the normal
	// CLI validation path when the required transport flag is omitted.
	{
		name: "rpc/missing-stdio",
		args: []string{"rpc"},
	},
	// server ✓ local daemon-control surface. Non-invasive status/stop/error paths
	// are byte-proven here; start/health/stop against a real daemon are covered by
	// installed smoke to avoid background process churn in the parity table.
	{
		name: "server/status-json",
		args: []string{"server", "--addr", "127.0.0.1:9", "status", "--json"},
		env:  []string{"WRKQ_LAUNCHD_LABEL=com.praesidium.wrkq-rpccli-test-never"},
	},
	{
		name: "server/status-default-json",
		args: []string{"server", "--addr", "127.0.0.1:9", "status"},
		env:  []string{"WRKQ_LAUNCHD_LABEL=com.praesidium.wrkq-rpccli-test-never"},
	},
	{
		name: "server/stop-not-running-json",
		args: []string{"server", "--addr", "127.0.0.1:9", "stop"},
		env:  []string{"WRKQ_LAUNCHD_LABEL=com.praesidium.wrkq-rpccli-test-never"},
	},
	{
		name: "server/start-conflicting-modes",
		args: []string{"server", "start", "--foreground", "--daemon"},
		env:  []string{"WRKQ_LAUNCHD_LABEL=com.praesidium.wrkq-rpccli-test-never"},
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

	// projects ✓ RPC-backed via wrkq.project.listView. It intentionally ignores
	// project-root scoping and lists only top-level project containers.
	{
		name:  "projects/default-ndjson",
		setup: [][]string{{"mkdir", "alpha"}, {"mkdir", "beta"}, {"mkdir", "alpha/child"}},
		args:  []string{"projects"},
	},
	{
		name:  "projects/json",
		setup: [][]string{{"mkdir", "alpha"}, {"mkdir", "beta"}},
		args:  []string{"projects", "--json"},
	},
	{
		name:  "projects/ndjson",
		setup: [][]string{{"mkdir", "alpha"}, {"mkdir", "beta"}},
		args:  []string{"projects", "--ndjson"},
	},
	{
		name:  "projects/table",
		setup: [][]string{{"mkdir", "alpha"}, {"mkdir", "beta"}},
		args:  []string{"projects", "--output", "table"},
	},
	{
		name:  "projects/yaml",
		setup: [][]string{{"mkdir", "alpha"}, {"mkdir", "beta"}},
		args:  []string{"projects", "--output", "yaml"},
	},
	{
		name:  "projects/tsv",
		setup: [][]string{{"mkdir", "alpha"}, {"mkdir", "beta"}},
		args:  []string{"projects", "--output", "tsv"},
	},
	{
		name:  "projects/one",
		setup: [][]string{{"mkdir", "alpha"}, {"mkdir", "beta"}},
		args:  []string{"projects", "-1"},
	},
	{
		name:  "projects/nul",
		setup: [][]string{{"mkdir", "alpha"}, {"mkdir", "beta"}},
		args:  []string{"projects", "-0"},
	},
	{
		name:  "projects/porcelain-limit-cursor",
		setup: [][]string{{"mkdir", "alpha"}, {"mkdir", "beta"}},
		args:  []string{"projects", "--porcelain", "--limit", "1"},
	},
	{
		name:  "projects/project-root-ignored",
		setup: [][]string{{"mkdir", "alpha"}, {"mkdir", "beta"}},
		args:  []string{"projects", "-1"},
		env:   []string{"WRKQ_PROJECT_ROOT=alpha"},
	},
	{
		name:  "projects/output-raw-unsupported",
		setup: [][]string{{"mkdir", "alpha"}},
		args:  []string{"projects", "--output", "raw"},
	},

	// cat ✓ RPC-backed via wrkq.task.catView (server-owned compat projection).
	// All exposed render modes are byte-proven: json (--json + non-TTY default),
	// ndjson (--ndjson), raw (--output raw / --porcelain / TTY default markdown),
	// and compact json (--json --porcelain).
	{
		name:  "cat/single-json",
		setup: [][]string{{"touch", "inbox/one", "-t", "One", "--priority", "2", "--labels", `["x","y"]`, "-d", "body Ã¢ÂÂ"}},
		args:  []string{"cat", "inbox/one", "--json"},
	},
	{
		name:  "cat/single-ndjson",
		setup: [][]string{{"touch", "inbox/one", "-t", "One", "--priority", "2", "--labels", `["x","y"]`, "-d", "body Ã¢ÂÂ"}},
		args:  []string{"cat", "inbox/one", "--ndjson"},
	},
	{
		name:  "cat/multi-ndjson",
		setup: [][]string{{"touch", "inbox/one", "-t", "One"}, {"touch", "inbox/two", "-t", "Two"}},
		args:  []string{"cat", "inbox/one", "inbox/two", "--ndjson"},
	},
	{
		name:  "cat/single-porcelain-json",
		setup: [][]string{{"touch", "inbox/one", "-t", "One", "--priority", "2"}},
		args:  []string{"cat", "inbox/one", "--json", "--porcelain"},
	},
	{
		name:  "cat/single-raw",
		setup: [][]string{{"touch", "inbox/one", "-t", "One", "--priority", "2", "--labels", `["x","y"]`, "-d", "body Ã¢ÂÂ"}},
		args:  []string{"cat", "inbox/one", "--output", "raw"},
	},
	{
		name:  "cat/single-porcelain-raw",
		setup: [][]string{{"touch", "inbox/one", "-t", "One", "-d", "porcelain body"}},
		args:  []string{"cat", "inbox/one", "--porcelain"},
	},
	{
		name:  "cat/multi-raw",
		setup: [][]string{{"touch", "inbox/one", "-t", "One", "-d", "first body"}, {"touch", "inbox/two", "-t", "Two", "-d", "second body"}},
		args:  []string{"cat", "inbox/one", "inbox/two", "--output", "raw"},
	},
	{
		name:  "cat/raw-no-frontmatter",
		setup: [][]string{{"touch", "inbox/one", "-t", "One", "-d", "just the body"}},
		args:  []string{"cat", "inbox/one", "--output", "raw", "--no-frontmatter"},
	},
	{
		name: "cat/raw-with-comment",
		setup: [][]string{
			{"touch", "inbox/cmt", "-t", "Commented", "-d", "main body"},
			{"comment", "add", "inbox/cmt", "first comment Ã¢ÂÂ"},
		},
		args: []string{"cat", "inbox/cmt", "--output", "raw"},
	},
	{
		name: "cat/raw-with-relation-and-blocker",
		setup: [][]string{
			{"touch", "inbox/main", "-t", "Main", "-d", "main desc"},
			{"touch", "inbox/blk", "-t", "Blocker"},
			{"relation", "add", "inbox/blk", "blocks", "inbox/main"},
		},
		args: []string{"cat", "inbox/main", "--output", "raw"},
	},
	// ── cat --pretty: the shared styled card forced non-TTY (E-pretty) ──
	// Both binaries call the SAME internal/style renderer, so output is identical
	// by construction. WRKQ_NOW pins the "updated … ago" relative phrase so the
	// freshly-seeded updated_at renders deterministically on both sides; color is
	// off (non-TTY) so the card is plain text and byte-comparable.
	{
		name:  "cat/pretty-basic",
		setup: [][]string{{"touch", "inbox/pretty", "-t", "Pretty", "-d", "Some **bold** body.\n- one\n- two"}},
		args:  []string{"cat", "inbox/pretty", "--pretty"},
		env:   []string{"WRKQ_NOW=2026-06-25T12:00:00Z"},
	},
	{
		name:  "cat/pretty",
		setup: [][]string{{"touch", "inbox/pretty", "-t", "Pretty", "-d", "Some **bold** body.\n- one\n- two"}},
		args:  []string{"cat", "inbox/pretty", "--pretty"},
		env:   []string{"WRKQ_NOW=2026-06-25T12:00:00Z"},
	},
	{
		name:  "cat/pretty-no-frontmatter",
		setup: [][]string{{"touch", "inbox/pretty", "-t", "Pretty", "-d", "body only"}},
		args:  []string{"cat", "inbox/pretty", "--pretty", "--no-frontmatter"},
		env:   []string{"WRKQ_NOW=2026-06-25T12:00:00Z"},
	},
	{
		name: "cat/pretty-with-comment",
		setup: [][]string{
			{"touch", "inbox/pretty", "-t", "Pretty", "-d", "main body"},
			{"comment", "add", "inbox/pretty", "a styled comment"},
		},
		args: []string{"cat", "inbox/pretty", "--pretty"},
		env:  []string{"WRKQ_NOW=2026-06-25T12:00:00Z"},
	},
	{
		name: "cat/pretty-with-blocker",
		setup: [][]string{
			{"touch", "inbox/main", "-t", "Main", "-d", "blocked desc"},
			{"touch", "inbox/blk", "-t", "Blocker"},
			{"relation", "add", "inbox/blk", "blocks", "inbox/main"},
		},
		args: []string{"cat", "inbox/main", "--pretty"},
		env:  []string{"WRKQ_NOW=2026-06-25T12:00:00Z"},
	},
	{
		// --pretty wins over an explicit machine mode (styled card, not JSON).
		name:  "cat/pretty-overrides-json",
		setup: [][]string{{"touch", "inbox/pretty", "-t", "Pretty", "-d", "body"}},
		args:  []string{"cat", "inbox/pretty", "--pretty", "--json"},
		env:   []string{"WRKQ_NOW=2026-06-25T12:00:00Z"},
	},
	{
		name:  "cat/raw-table-not-supported",
		setup: [][]string{{"touch", "inbox/one", "-t", "One"}},
		args:  []string{"cat", "inbox/one", "--output", "table"},
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

	// diff ✓ RPC-backed: two wrkq.task.catView reads, CLI-local field comparison
	// + JSON/human rendering. Default (non-TTY) output is JSON, matching legacy.
	{
		name: "diff/two-tasks-json",
		setup: [][]string{
			{"touch", "inbox/da", "-t", "Title A", "--priority", "2", "-d", "desc a"},
			{"touch", "inbox/db", "-t", "Title B", "--priority", "1", "-d", "desc b"},
		},
		args: []string{"diff", "inbox/da", "inbox/db", "--json"},
	},
	{
		name: "diff/two-tasks-default-json",
		setup: [][]string{
			{"touch", "inbox/da", "-t", "Title A"},
			{"touch", "inbox/db", "-t", "Title B"},
		},
		args: []string{"diff", "inbox/da", "inbox/db"},
	},
	{
		name: "diff/same-title-only-slug-differs",
		setup: [][]string{
			{"touch", "inbox/da", "-t", "Same"},
			{"touch", "inbox/db", "-t", "Same"},
		},
		args: []string{"diff", "inbox/da", "inbox/db", "--json"},
	},
	{
		name:  "diff/single-arg-not-implemented",
		setup: [][]string{{"touch", "inbox/da", "-t", "Only A"}},
		args:  []string{"diff", "inbox/da"},
	},
	{
		name:  "diff/unknown-ref-A-errors",
		setup: nil,
		args:  []string{"diff", "T-09999999", "T-09999998", "--json"},
	},
	{
		name:  "diff/unknown-ref-B-errors",
		setup: [][]string{{"touch", "inbox/da", "-t", "Only A"}},
		args:  []string{"diff", "inbox/da", "T-09999998", "--json"},
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
	// rmdir --force ✓ two-phase wrkq.container.deleteRecursive on the
	// caller-owned-confirmation seam: a dryRun preflight returns the impact, the
	// mirror renders the legacy WARNING block + prompts "Are you sure? (yes/no):"
	// (requiring exact "yes"), then commits echoing expected:{...} (CAS race
	// guard). Single-level non-empty containers are byte-proven (immediate counts
	// == recursive impact); the prompt only renders for non-empty.
	{
		// --force --yes: non-empty container, prompt skipped, recursive delete.
		name: "rmdir/force-yes",
		setup: [][]string{
			{"mkdir", "doomed"},
			{"touch", "doomed/t1", "-t", "One"},
			{"touch", "doomed/t2", "-t", "Two"},
		},
		args:    []string{"rmdir", "doomed", "--force", "--yes"},
		mutates: true,
	},
	{
		// --force prompt ACCEPT via stdin "yes": WARNING block + confirm line + delete.
		name: "rmdir/force-prompt-accept",
		setup: [][]string{
			{"mkdir", "doomed"},
			{"touch", "doomed/t1", "-t", "One"},
		},
		args:    []string{"rmdir", "doomed", "--force"},
		stdin:   []byte("yes\n"),
		mutates: true,
	},
	{
		// --force prompt ABORT via stdin "no": WARNING + "aborted" error, no mutation.
		name: "rmdir/force-prompt-abort",
		setup: [][]string{
			{"mkdir", "doomed"},
			{"touch", "doomed/t1", "-t", "One"},
		},
		args:    []string{"rmdir", "doomed", "--force"},
		stdin:   []byte("no\n"),
		mutates: true,
	},
	{
		// --force prompt EMPTY stdin (non-TTY, no input): EOF → abort, no hang/mutation.
		name: "rmdir/force-prompt-empty-stdin-abort",
		setup: [][]string{
			{"mkdir", "doomed"},
			{"touch", "doomed/t1", "-t", "One"},
		},
		args:    []string{"rmdir", "doomed", "--force"},
		stdin:   []byte(""),
		mutates: true,
	},
	{
		// --force on an EMPTY container: no prompt (nothing to delete recursively),
		// recursive path still removes it. Proves force+empty doesn't prompt.
		name:    "rmdir/force-empty-no-prompt",
		setup:   [][]string{{"mkdir", "hollow"}},
		args:    []string{"rmdir", "hollow", "--force"},
		mutates: true,
	},

	// rename-container ✓ NEW server method wrkq.container.update (narrow {slug,title}
	// patch + etag CAS, identity-preserving; T-05112). NON-destructive (no prompt).
	// The mirror owns dry-run rendering + project-root scoping; slug
	// normalization/validation, CAS, attribution + container.updated are
	// server-owned. The durable snapshot proves the slug change survives in place.
	{
		// Default: title defaults to the new slug; slug + title both change in place.
		name:    "rename-container/default-title",
		setup:   [][]string{{"mkdir", "oldproj"}},
		args:    []string{"rename-container", "oldproj", "newproj"},
		mutates: true,
	},
	{
		// --title: a custom title overrides the new-slug default.
		name:    "rename-container/custom-title",
		setup:   [][]string{{"mkdir", "oldproj"}},
		args:    []string{"rename-container", "oldproj", "newproj", "--title", "Brand New Project"},
		mutates: true,
	},
	{
		// dry-run (non-TTY): renders the JSON preview, NO mutation.
		name:    "rename-container/dry-run",
		setup:   [][]string{{"mkdir", "oldproj"}},
		args:    []string{"rename-container", "oldproj", "newproj", "--dry-run"},
		mutates: true,
	},
	{
		// dry-run with --title (non-TTY): preview echoes the custom title, NO mutation.
		name:    "rename-container/dry-run-custom-title",
		setup:   [][]string{{"mkdir", "oldproj"}},
		args:    []string{"rename-container", "oldproj", "newproj", "--dry-run", "--title", "Preview Title"},
		mutates: true,
	},
	{
		// --if-match with the matching etag: succeeds. A freshly-created container
		// has etag 1.
		name:    "rename-container/if-match-ok",
		setup:   [][]string{{"mkdir", "oldproj"}},
		args:    []string{"rename-container", "oldproj", "newproj", "--if-match", "1"},
		mutates: true,
	},
	{
		// --if-match with a STALE etag: server CAS rejects (WRKQ_CONFLICT), no mutation.
		name:    "rename-container/if-match-stale",
		setup:   [][]string{{"mkdir", "oldproj"}},
		args:    []string{"rename-container", "oldproj", "newproj", "--if-match", "99"},
		mutates: true,
	},
	{
		// Unknown container: legacy "container not found: <selector>", no mutation.
		name:    "rename-container/unknown-container",
		setup:   [][]string{{"mkdir", "oldproj"}},
		args:    []string{"rename-container", "ghostproj", "newproj"},
		mutates: true,
	},
	{
		// Invalid slug: client-side NormalizeSlug rejects with the legacy wording.
		name:    "rename-container/invalid-slug",
		setup:   [][]string{{"mkdir", "oldproj"}},
		args:    []string{"rename-container", "oldproj", "!!!"},
		mutates: true,
	},
	// NOTE: slug-conflict is a DELIBERATE byte divergence, not a parity case.
	// Legacy LEAKS the raw SQLite error ("failed to update container: UNIQUE
	// constraint failed: containers.parent_uuid, containers.slug"); the RPC path
	// returns a STABLE, implementation-free WRKQ_CONFLICT message ("container slug
	// already exists in parent") per the T-05112 daedalus ruling (hrcchat#10203).
	// The typed code + clean (no SQLite/store text) message are asserted in the
	// server acceptance test TestWrkqContainerUpdate_SlugConflictTyped, so this is
	// intentionally absent from the byte-equality table.
	{
		// project-root scoping: a relative selector is resolved under WRKQ_PROJECT_ROOT
		// before becoming an RPC param. Rename a child container of myproj.
		name:    "rename-container/project-root-scoping",
		setup:   [][]string{{"mkdir", "myproj"}, {"mkdir", "myproj/child"}},
		args:    []string{"rename-container", "child", "renamed-child"},
		env:     []string{"WRKQ_PROJECT_ROOT=myproj"},
		mutates: true,
	},

	// webhook ✓ DEDICATED family (wrkq.webhook.add/remove/listView, T-05119
	// daedalus #10211). The GLOBAL webhook URLs live on the SINGLETON ROOT
	// container; the server owns root resolution, URL validation, the idempotent
	// add/remove delta, the webhook_urls write + attribution + container.updated
	// event. The mirror owns ONLY output rendering. NOT wrkq.container.update
	// (which rejects webhookUrls). No project scoping, no --if-match.
	{
		// list (non-TTY): empty → no rows emitted on either binary.
		name: "webhook/list-empty",
		args: []string{"webhook", "list"},
	},
	{
		// list (non-TTY): one {"url":...} NDJSON line per stored URL, stored order.
		name: "webhook/list-populated",
		setup: [][]string{
			{"webhook", "add", "https://a.test/wrkq"},
			{"webhook", "add", "https://b.test/wrkq"},
		},
		args: []string{"webhook", "list"},
	},
	{
		// add (non-TTY): indented JSON mutation result in MAP-ALPHABETICAL key
		// order {changed,count,target,webhook_urls}; durable webhook_urls write.
		name:    "webhook/add",
		args:    []string{"webhook", "add", "https://hook.test/wrkq"},
		mutates: true,
	},
	{
		// add with an explicit canonical principal (--as agent:flag-principal, no
		// legacy actor row): the durable updated_by_principal_ref in the snapshot
		// must record agent:flag-principal on BOTH binaries (not wrkq-system). This
		// is the cross-binary attribution guard for daedalus #10261 (T-05119); the
		// rendered result is also byte-checked.
		name:    "webhook/add-with-principal",
		args:    []string{"webhook", "add", "https://hook.test/wrkq", "--as", "agent:flag-principal"},
		mutates: true,
	},
	{
		// add duplicate (non-TTY): idempotent → no-change result {changed,webhook_urls}.
		name:    "webhook/add-duplicate-no-change",
		setup:   [][]string{{"webhook", "add", "https://hook.test/wrkq"}},
		args:    []string{"webhook", "add", "https://hook.test/wrkq"},
		mutates: true,
	},
	{
		// add invalid URL: server validation rejects with legacy wording
		// "invalid webhook url: <url>" (no domain-code prefix leak).
		name:    "webhook/add-invalid-url",
		args:    []string{"webhook", "add", "not-a-url"},
		mutates: true,
	},
	{
		// rm (non-TTY): removes an existing URL → changed result; durable write.
		name:    "webhook/rm",
		setup:   [][]string{{"webhook", "add", "https://hook.test/wrkq"}},
		args:    []string{"webhook", "rm", "https://hook.test/wrkq"},
		mutates: true,
	},
	{
		// rm missing URL (non-TTY): idempotent → no-change result {changed,webhook_urls}.
		name:    "webhook/rm-missing-no-change",
		args:    []string{"webhook", "rm", "https://absent.test/wrkq"},
		mutates: true,
	},

	// rm ✓ caller-owned-confirmation seam. Task mutation runs through wrkq.task.delete
	// with an EXPLICIT mode (archive default / purge for --purge). Container mutation
	// uses wrkq.container.archive for the default soft-delete and wrkq.container.delete
	// for empty-container purge. The mirror owns scoping, purge prompt+abort+--yes,
	// dry-run, and exit-code taxonomy.
	{
		// Default = legacy soft-archive (state=archived + archived_at via mode:archive).
		name:    "rm/archive-default",
		setup:   [][]string{{"touch", "inbox/aaa", "-t", "Alpha", "--priority", "2"}},
		args:    []string{"rm", "inbox/aaa"},
		mutates: true,
	},
	{
		// Multi-target archive: ordering + both rows archived.
		name: "rm/archive-multi",
		setup: [][]string{
			{"touch", "inbox/aaa", "-t", "Alpha"},
			{"touch", "inbox/bbb", "-t", "Beta"},
		},
		args:    []string{"rm", "inbox/aaa", "inbox/bbb"},
		mutates: true,
	},
	{
		// --purge --yes hard-deletes the task row (no prompt because --yes).
		name:    "rm/purge-yes",
		setup:   [][]string{{"touch", "inbox/aaa", "-t", "Alpha"}},
		args:    []string{"rm", "inbox/aaa", "--purge", "--yes"},
		mutates: true,
	},
	{
		// --purge prompt ACCEPT via stdin "yes": warning text + confirm line + delete.
		name:    "rm/purge-prompt-accept",
		setup:   [][]string{{"touch", "inbox/aaa", "-t", "Alpha"}},
		args:    []string{"rm", "inbox/aaa", "--purge"},
		stdin:   []byte("yes\n"),
		mutates: true,
	},
	{
		// --purge prompt ABORT via stdin "no": warning + "aborted" error, no mutation.
		name:    "rm/purge-prompt-abort",
		setup:   [][]string{{"touch", "inbox/aaa", "-t", "Alpha"}},
		args:    []string{"rm", "inbox/aaa", "--purge"},
		stdin:   []byte("no\n"),
		mutates: true,
	},
	{
		// --purge prompt with EMPTY stdin (non-TTY, no input): EOF → abort, no hang.
		name:    "rm/purge-prompt-empty-stdin-abort",
		setup:   [][]string{{"touch", "inbox/aaa", "-t", "Alpha"}},
		args:    []string{"rm", "inbox/aaa", "--purge"},
		stdin:   []byte(""),
		mutates: true,
	},
	{
		// Non-TTY dry-run emits the legacy JSON plan (archive variant).
		name:  "rm/dry-run-json-archive",
		setup: [][]string{{"touch", "inbox/aaa", "-t", "Alpha", "--priority", "2"}},
		args:  []string{"rm", "inbox/aaa", "--dry-run"},
	},
	{
		// Non-TTY dry-run emits the legacy JSON plan (purge variant: purge:true).
		name:  "rm/dry-run-json-purge",
		setup: [][]string{{"touch", "inbox/aaa", "-t", "Alpha", "--priority", "2"}},
		args:  []string{"rm", "inbox/aaa", "--purge", "--dry-run"},
	},
	{
		name: "rm/ndjson-multi",
		setup: [][]string{
			{"touch", "inbox/aaa", "-t", "Alpha"},
			{"touch", "inbox/bbb", "-t", "Beta"},
		},
		args:    []string{"rm", "inbox/aaa", "inbox/bbb", "--ndjson"},
		mutates: true,
	},
	{
		name: "rm/porcelain-multi",
		setup: [][]string{
			{"touch", "inbox/aaa", "-t", "Alpha"},
			{"touch", "inbox/bbb", "-t", "Beta"},
		},
		args:    []string{"rm", "inbox/aaa", "inbox/bbb", "--porcelain"},
		mutates: true,
	},
	{
		// Unknown ref WITHOUT nullglob → "target not found" error, exit 1, no mutation.
		name:    "rm/notfound-no-nullglob",
		setup:   [][]string{{"touch", "inbox/aaa", "-t", "Alpha"}},
		args:    []string{"rm", "inbox/zzz"},
		mutates: true,
	},
	{
		// Unknown ref WITH --nullglob → no-op exit 0, no mutation.
		name:    "rm/notfound-nullglob",
		setup:   [][]string{{"touch", "inbox/aaa", "-t", "Alpha"}},
		args:    []string{"rm", "inbox/zzz", "--nullglob"},
		mutates: true,
	},
	{
		// Mixed found + unknown with --nullglob: the found task is archived, the
		// unknown is skipped (exit 0).
		name: "rm/nullglob-mixed",
		setup: [][]string{
			{"touch", "inbox/aaa", "-t", "Alpha"},
		},
		args:    []string{"rm", "inbox/aaa", "inbox/zzz", "--nullglob"},
		mutates: true,
	},
	{
		// Partial failure with --continue-on-error: one resolvable archive + one that
		// cannot be removed; legacy exits 5 on partial success. Here both resolve, so
		// to exercise the taxonomy we re-rm an already-archived task (store no-op) plus
		// an unknown caught at resolve time — resolution errors abort before exec, so
		// this case asserts the resolve-time error path is identical.
		name: "rm/stdin-dash-refs",
		setup: [][]string{
			{"touch", "inbox/aaa", "-t", "Alpha"},
			{"touch", "inbox/bbb", "-t", "Beta"},
		},
		args:    []string{"rm", "-"},
		stdin:   []byte("inbox/aaa\ninbox/bbb\n"),
		mutates: true,
	},
	{
		// project-root scoping: relative selector resolved under WRKQ_PROJECT_ROOT.
		name:    "rm/pr-relative-selector",
		setup:   prSeed,
		args:    []string{"rm", "task-a"},
		env:     []string{"WRKQ_PROJECT_ROOT=myproj"},
		mutates: true,
	},
	{
		name:    "rm/container-archive-default",
		setup:   [][]string{{"mkdir", "doomed"}},
		args:    []string{"rm", "doomed"},
		mutates: true,
	},
	{
		name:  "rm/container-dry-run-json",
		setup: [][]string{{"mkdir", "doomed"}},
		args:  []string{"rm", "doomed", "--dry-run"},
	},
	{
		name:    "rm/container-purge-empty-yes",
		setup:   [][]string{{"mkdir", "doomed"}},
		args:    []string{"rm", "doomed", "--purge", "--yes"},
		mutates: true,
	},
	{
		name:    "rm/container-purge-prompt-accept",
		setup:   [][]string{{"mkdir", "doomed"}},
		args:    []string{"rm", "doomed", "--purge"},
		stdin:   []byte("yes\n"),
		mutates: true,
	},
	{
		name:    "rm/container-purge-nonempty-errors",
		setup:   [][]string{{"mkdir", "doomed"}, {"touch", "doomed/child", "-t", "Child"}},
		args:    []string{"rm", "doomed", "--purge", "--yes"},
		mutates: true,
	},
	{
		name: "rm/mixed-task-container-archive",
		setup: [][]string{
			{"touch", "inbox/aaa", "-t", "Alpha"},
			{"mkdir", "doomed"},
		},
		args:    []string{"rm", "inbox/aaa", "doomed"},
		mutates: true,
	},
	{
		// --recursive and --force are accepted by legacy rm but recursive container
		// deletion is deliberately the rmdir --force surface. For task archives these
		// flags are parsed and ignored.
		name:    "rm/recursive-force-task-archive",
		setup:   [][]string{{"touch", "inbox/aaa", "-t", "Alpha"}},
		args:    []string{"rm", "inbox/aaa", "--recursive", "--force"},
		mutates: true,
	},
	{
		// --jobs threads through the shared bulk executor. --ordered is not an rm
		// flag, so jobs-only is the remaining bulk-flag compatibility surface here.
		name: "rm/jobs-multi",
		setup: [][]string{
			{"touch", "inbox/aaa", "-t", "Alpha"},
			{"touch", "inbox/bbb", "-t", "Beta"},
		},
		args:    []string{"rm", "inbox/aaa", "inbox/bbb", "--jobs", "2"},
		mutates: true,
	},

	// restore ✓ RPC-backed via the EXTENDED wrkq.task.restore for tasks (server
	// carries move-on-restore, field updates, --comment, --state, cascade,
	// slug-conflict/etag precedence) and wrkq.container.restore for containers.
	{
		name:    "restore/basic-archived",
		setup:   [][]string{{"touch", "inbox/gone", "-t", "Gone"}, {"rm", "inbox/gone"}},
		args:    []string{"restore", "inbox/gone"},
		mutates: true,
	},
	{
		name:    "restore/by-id",
		setup:   [][]string{{"touch", "inbox/gone", "-t", "Gone"}, {"rm", "inbox/gone"}},
		args:    []string{"restore", "T-00001"},
		mutates: true,
	},
	{
		name:    "restore/state",
		setup:   [][]string{{"touch", "inbox/gone", "-t", "Gone"}, {"rm", "inbox/gone"}},
		args:    []string{"restore", "inbox/gone", "--state", "in_progress"},
		mutates: true,
	},
	{
		name:    "restore/comment",
		setup:   [][]string{{"touch", "inbox/gone", "-t", "Gone"}, {"rm", "inbox/gone"}},
		args:    []string{"restore", "inbox/gone", "--comment", "back from the dead"},
		mutates: true,
	},
	{
		name:    "restore/title",
		setup:   [][]string{{"touch", "inbox/gone", "-t", "Gone"}, {"rm", "inbox/gone"}},
		args:    []string{"restore", "inbox/gone", "--title", "Renamed On Restore"},
		mutates: true,
	},
	{
		name:    "restore/description",
		setup:   [][]string{{"touch", "inbox/gone", "-t", "Gone"}, {"rm", "inbox/gone"}},
		args:    []string{"restore", "inbox/gone", "--description", "fresh body"},
		mutates: true,
	},
	{
		name:    "restore/priority",
		setup:   [][]string{{"touch", "inbox/gone", "-t", "Gone", "--priority", "3"}, {"rm", "inbox/gone"}},
		args:    []string{"restore", "inbox/gone", "--priority", "1"},
		mutates: true,
	},
	{
		name:    "restore/labels",
		setup:   [][]string{{"touch", "inbox/gone", "-t", "Gone"}, {"rm", "inbox/gone"}},
		args:    []string{"restore", "inbox/gone", "--labels", `["urgent","x"]`},
		mutates: true,
	},
	{
		name:    "restore/assignee",
		setup:   [][]string{{"touch", "inbox/gone", "-t", "Gone"}, {"rm", "inbox/gone"}},
		args:    []string{"restore", "inbox/gone", "--assignee", "clod"},
		mutates: true,
	},
	{
		// --to: move-on-restore into an existing destination container (new slug).
		name: "restore/to-move",
		setup: [][]string{
			{"mkdir", "dest"},
			{"touch", "inbox/gone", "-t", "Gone"},
			{"rm", "inbox/gone"},
		},
		args:    []string{"restore", "inbox/gone", "--to", "dest/gone"},
		mutates: true,
	},
	{
		// --to slug conflict: destination already has a task with that slug.
		name: "restore/to-slug-conflict",
		setup: [][]string{
			{"mkdir", "dest"},
			{"touch", "dest/taken", "-t", "Taken"},
			{"touch", "inbox/gone", "-t", "Gone"},
			{"rm", "inbox/gone"},
		},
		args:    []string{"restore", "inbox/gone", "--to", "dest/taken"},
		mutates: true,
	},
	{
		// --if-match matching etag → restore proceeds.
		name:    "restore/if-match-ok",
		setup:   [][]string{{"touch", "inbox/gone", "-t", "Gone"}, {"rm", "inbox/gone"}},
		args:    []string{"restore", "inbox/gone", "--if-match", "1"},
		mutates: true,
	},
	{
		// --if-match mismatch → etag mismatch error, no mutation.
		name:    "restore/if-match-mismatch",
		setup:   [][]string{{"touch", "inbox/gone", "-t", "Gone"}, {"rm", "inbox/gone"}},
		args:    []string{"restore", "inbox/gone", "--if-match", "999"},
		mutates: true,
	},
	{
		// Cascade restore: archiving a parent archives subtasks; restoring the parent
		// cascade-restores them.
		name: "restore/cascade-subtasks",
		setup: [][]string{
			{"touch", "inbox/parent", "-t", "Parent"},
			{"touch", "inbox/child", "-t", "Child", "--parent-task", "inbox/parent"},
			{"rm", "inbox/child"},
			{"rm", "inbox/parent"},
		},
		args:    []string{"restore", "inbox/parent"},
		mutates: true,
	},
	{
		// Restore-to-archived-state is rejected (precedence: validated before DB read).
		name:    "restore/reject-archived-state",
		setup:   [][]string{{"touch", "inbox/gone", "-t", "Gone"}, {"rm", "inbox/gone"}},
		args:    []string{"restore", "inbox/gone", "--state", "archived"},
		mutates: true,
	},
	{
		// Not-deleted-or-archived task cannot be restored.
		name:    "restore/reject-active",
		setup:   [][]string{{"touch", "inbox/live", "-t", "Live"}},
		args:    []string{"restore", "inbox/live"},
		mutates: true,
	},
	{
		name:    "restore/not-found",
		setup:   nil,
		args:    []string{"restore", "T-09999999"},
		mutates: true,
	},
	{
		// Validation-before-resolution precedence: a bad flag on a MISSING ref must
		// fail with the VALIDATION error, NOT not-found/container-gate. The mirror
		// calls wrkq.task.restore first (no speculative task.show), so the server's
		// flag-validation-before-lookup ordering is preserved end to end.
		name:    "restore/precedence-bad-state-missing-ref",
		setup:   nil,
		args:    []string{"restore", "T-09999999", "--state", "archived"},
		mutates: true,
	},
	{
		name:    "restore/precedence-bad-priority-missing-ref",
		setup:   nil,
		args:    []string{"restore", "T-09999999", "--priority", "99"},
		mutates: true,
	},
	{
		name:    "restore/precedence-bad-labels-missing-ref",
		setup:   nil,
		args:    []string{"restore", "T-09999999", "--labels", "not-json"},
		mutates: true,
	},
	{
		name:    "restore/precedence-bad-assignee-missing-ref",
		setup:   nil,
		args:    []string{"restore", "T-09999999", "--assignee", "agent:x:project:y"},
		mutates: true,
	},
	{
		// project-root scoping: relative selector resolved under WRKQ_PROJECT_ROOT.
		name:    "restore/pr-relative-selector",
		setup:   [][]string{{"mkdir", "myproj"}, {"touch", "myproj/task-a", "-t", "A"}, {"rm", "myproj/task-a"}},
		args:    []string{"restore", "task-a"},
		env:     []string{"WRKQ_PROJECT_ROOT=myproj"},
		mutates: true,
	},
	{
		name:    "restore/container-archived",
		setup:   [][]string{{"mkdir", "archivedproj"}, {"rm", "archivedproj"}},
		args:    []string{"restore", "archivedproj"},
		mutates: true,
	},
	{
		name:    "restore/container-by-id",
		setup:   [][]string{{"mkdir", "archivedproj"}, {"rm", "archivedproj"}},
		args:    []string{"restore", "P-00002"},
		mutates: true,
	},

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
		name:          "touch/description-file",
		setup:         nil,
		args:          []string{"touch", "inbox/from-file", "-t", "From File", "-d", "@body.md"},
		files:         map[string]string{"body.md": "file-backed description\n\nwith paragraphs\n"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		name:          "touch/stdin-description",
		setup:         nil,
		args:          []string{"touch", "inbox/from-stdin", "-t", "From Stdin", "-d", "-"},
		stdin:         []byte("stdin-backed description\n"),
		mutates:       true,
		normalizeUUID: true,
	},
	{
		name:          "touch/meta-file",
		setup:         nil,
		args:          []string{"touch", "inbox/meta-file", "-t", "Meta File", "--meta-file", "meta.json"},
		files:         map[string]string{"meta.json": `{"source":"file","rank":2}`},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		name:          "touch/date-routing-resolution",
		setup:         nil,
		args:          []string{"touch", "inbox/routed", "-t", "Routed", "--due-at", "2026-07-01", "--start-at", "2026-06-30", "--requested-by", "wrkq", "--assigned-project", "agent-spaces", "--resolution", "needs_info"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		name:    "touch/force-uuid",
		setup:   nil,
		args:    []string{"touch", "inbox/forced", "-t", "Forced", "--force-uuid", "550e8400-e29b-41d4-a716-446655440000"},
		mutates: true,
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
	{
		name:    "mv/task-into-container-dry-run",
		setup:   [][]string{{"touch", "inbox/movable", "-t", "M"}, {"mkdir", "dest"}},
		args:    []string{"mv", "inbox/movable", "dest", "--dry-run"},
		mutates: false,
	},
	{
		name:    "mv/task-into-container-if-match",
		setup:   [][]string{{"touch", "inbox/movable", "-t", "M"}, {"mkdir", "dest"}},
		args:    []string{"mv", "inbox/movable", "dest", "--if-match", "2"},
		mutates: true,
	},
	{
		name:    "mv/task-rename",
		setup:   [][]string{{"touch", "inbox/old-name", "-t", "Old"}},
		args:    []string{"mv", "inbox/old-name", "inbox/new-name"},
		mutates: true,
	},
	{
		name:    "mv/task-rename-dry-run",
		setup:   [][]string{{"touch", "inbox/old-dry", "-t", "Old Dry"}},
		args:    []string{"mv", "inbox/old-dry", "inbox/new-dry", "--dry-run"},
		mutates: false,
	},
	{
		name:    "mv/task-move-new-path",
		setup:   [][]string{{"touch", "inbox/move-new-path", "-t", "Move"}, {"mkdir", "dest"}},
		args:    []string{"mv", "inbox/move-new-path", "dest/moved-new-path"},
		mutates: true,
	},
	{
		name:    "mv/task-overwrite",
		setup:   [][]string{{"touch", "inbox/source-overwrite", "-t", "Source"}, {"touch", "inbox/dest-overwrite", "-t", "Dest"}},
		args:    []string{"mv", "inbox/source-overwrite", "inbox/dest-overwrite", "--overwrite-task"},
		mutates: true,
	},
	{
		name:    "mv/multi-task-into-container",
		setup:   [][]string{{"touch", "inbox/multi-a", "-t", "A"}, {"touch", "inbox/multi-b", "-t", "B"}, {"mkdir", "dest"}},
		args:    []string{"mv", "inbox/multi-a", "inbox/multi-b", "dest"},
		mutates: true,
	},
	{
		name:    "mv/container-into-container",
		setup:   [][]string{{"mkdir", "inbox/src"}, {"touch", "inbox/src/child", "-t", "Child"}, {"mkdir", "dest"}},
		args:    []string{"mv", "inbox/src", "dest"},
		mutates: true,
	},
	{
		name:  "mv/container-into-container-dry-run",
		setup: [][]string{{"mkdir", "inbox/src"}, {"mkdir", "dest"}},
		args:  []string{"mv", "inbox/src", "dest", "--dry-run"},
	},
	{
		name:    "mv/container-rename-nested",
		setup:   [][]string{{"mkdir", "proj"}, {"mkdir", "proj/old-name"}, {"touch", "proj/old-name/child", "-t", "Child"}},
		args:    []string{"mv", "proj/old-name", "proj/new-name"},
		mutates: true,
	},
	{
		name:    "mv/container-move-new-path",
		setup:   [][]string{{"mkdir", "proj"}, {"mkdir", "proj/old-name"}, {"touch", "proj/old-name/child", "-t", "Child"}, {"mkdir", "dest"}},
		args:    []string{"mv", "proj/old-name", "dest/new-name"},
		mutates: true,
	},
	{
		name:  "mv/container-dest-conflict-errors",
		setup: [][]string{{"mkdir", "proj"}, {"mkdir", "proj/old-name"}, {"mkdir", "proj/new-name"}},
		args:  []string{"mv", "proj/old-name", "proj/new-name"},
	},
	{
		name:  "mv/container-top-level-rename-legacy-error",
		setup: [][]string{{"mkdir", "proj"}},
		args:  []string{"mv", "proj", "newproj"},
	},
	{
		name:    "mv/multi-task-and-container-into-container",
		setup:   [][]string{{"touch", "inbox/multi-a", "-t", "A"}, {"mkdir", "inbox/src"}, {"touch", "inbox/src/child", "-t", "Child"}, {"mkdir", "dest"}},
		args:    []string{"mv", "inbox/multi-a", "inbox/src", "dest"},
		mutates: true,
	},
	{
		// Legacy parses --type/--yes but runMv never consults them.
		name:    "mv/type-and-yes-ignored-task",
		setup:   [][]string{{"touch", "inbox/typed-task", "-t", "Typed"}, {"mkdir", "dest"}},
		args:    []string{"mv", "inbox/typed-task", "dest", "--type", "p", "--yes"},
		mutates: true,
	},
	{
		name:    "mv/type-and-yes-ignored-container",
		setup:   [][]string{{"mkdir", "inbox/typed-container"}, {"mkdir", "dest"}},
		args:    []string{"mv", "inbox/typed-container", "dest", "--type", "t", "--yes"},
		mutates: true,
	},
	{
		// Legacy parses --nullglob on mv but never consults it.
		name:  "mv/nullglob-missing-source-still-errors",
		setup: [][]string{{"mkdir", "dest"}},
		args:  []string{"mv", "missing-source", "dest", "--nullglob"},
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
	{
		name:    "set/dry-run-json",
		setup:   [][]string{{"touch", "inbox/sdry", "-t", "Dry"}},
		args:    []string{"set", "inbox/sdry", "--state", "in_progress", "--dry-run"},
		mutates: false,
	},
	{
		name:    "set/if-match-ok",
		setup:   [][]string{{"touch", "inbox/scas", "-t", "CAS"}},
		args:    []string{"set", "inbox/scas", "--state", "in_progress", "--if-match", "2"},
		mutates: true,
	},
	{
		name:    "set/description-file",
		setup:   [][]string{{"touch", "inbox/sfile", "-t", "File"}},
		args:    []string{"set", "inbox/sfile", "--description", "@body.md"},
		files:   map[string]string{"body.md": "updated from file\n"},
		mutates: true,
	},
	{
		name:    "set/stdin-refs",
		setup:   [][]string{{"touch", "inbox/stdin-a", "-t", "A"}, {"touch", "inbox/stdin-b", "-t", "B"}},
		args:    []string{"set", "-", "--state", "in_progress", "--continue-on-error"},
		stdin:   []byte("inbox/stdin-a\ninbox/stdin-b\n"),
		mutates: true,
	},
	{
		name:    "set/meta-file",
		setup:   [][]string{{"touch", "inbox/smeta", "-t", "Meta"}},
		args:    []string{"set", "inbox/smeta", "--meta-file", "meta.json"},
		files:   map[string]string{"meta.json": `{"phase":"rpccli","ok":true}`},
		mutates: true,
	},
	{
		name:    "set/slug-assignee",
		setup:   [][]string{{"touch", "inbox/sidentity", "-t", "Identity"}},
		args:    []string{"set", "inbox/sidentity", "--slug", "New Identity", "--assignee", "cody"},
		mutates: true,
	},
	{
		name:    "set/routing-resolution",
		setup:   [][]string{{"touch", "inbox/sroute", "-t", "Route"}},
		args:    []string{"set", "inbox/sroute", "--requested-by", "P-REQ", "--assigned-project", "P-ASG", "--resolution", "done"},
		mutates: true,
	},
	{
		name:    "set/parent-task",
		setup:   [][]string{{"touch", "inbox/parent", "-t", "Parent"}, {"touch", "inbox/child", "-t", "Child"}},
		args:    []string{"set", "inbox/child", "--parent-task", "inbox/parent"},
		mutates: true,
	},
	{
		name:    "set/parent-clear",
		setup:   [][]string{{"touch", "inbox/parent", "-t", "Parent"}, {"touch", "inbox/child", "-t", "Child", "--parent-task", "inbox/parent"}},
		args:    []string{"set", "inbox/child", "--parent-id", ""},
		mutates: true,
	},
	{
		name:    "set/parent-dry-run-json",
		setup:   [][]string{{"touch", "inbox/parent", "-t", "Parent"}, {"touch", "inbox/child", "-t", "Child"}},
		args:    []string{"set", "inbox/child", "--parent-task", "inbox/parent", "--dry-run"},
		mutates: false,
	},
	{
		// Bulk flags are legacy-accepted. --ordered forces sequential execution,
		// --jobs controls the bulk executor, and --batch-size is parsed but unused in
		// legacy; all three must be accepted by the mirror.
		name:    "set/bulk-flags",
		setup:   [][]string{{"touch", "inbox/bulk-a", "-t", "A"}, {"touch", "inbox/bulk-b", "-t", "B"}},
		args:    []string{"set", "inbox/bulk-a", "inbox/bulk-b", "--state", "completed", "--jobs", "2", "--ordered", "--batch-size", "4"},
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

	// comment rm ✓ caller-owned-confirmation seam via wrkq.comment.delete with an
	// EXPLICIT mode (soft default / purge for --purge). The mirror owns the [y/N]
	// prompt (accept y/Y, prompts EVEN for soft-delete), abort/--yes, --if-match
	// warn-skip, unknown-ref warn-continue, dry-run, and the non-TTY JSON array.
	{
		// Soft-delete default via --yes (no prompt): sets deleted_at + bumps etag.
		name:          "comment-rm/soft-yes",
		setup:         [][]string{{"touch", "inbox/cr", "-t", "CR"}, {"comment", "add", "inbox/cr", "body one"}},
		args:          []string{"comment", "rm", "C-00001", "--yes"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// --purge --yes: hard-delete the comment row.
		name:          "comment-rm/purge-yes",
		setup:         [][]string{{"touch", "inbox/cr", "-t", "CR"}, {"comment", "add", "inbox/cr", "body one"}},
		args:          []string{"comment", "rm", "C-00001", "--purge", "--yes"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// Prompt ACCEPT via stdin "y": soft-delete proceeds.
		name:          "comment-rm/prompt-accept-y",
		setup:         [][]string{{"touch", "inbox/cr", "-t", "CR"}, {"comment", "add", "inbox/cr", "body one"}},
		args:          []string{"comment", "rm", "C-00001"},
		stdin:         []byte("y\n"),
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// Prompt ABORT via stdin "n": per-comment skip, no mutation. Empty result array.
		name:          "comment-rm/prompt-abort-n",
		setup:         [][]string{{"touch", "inbox/cr", "-t", "CR"}, {"comment", "add", "inbox/cr", "body one"}},
		args:          []string{"comment", "rm", "C-00001"},
		stdin:         []byte("n\n"),
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// Prompt EMPTY stdin (non-TTY, no input): EOF → declined → skip, no hang.
		name:          "comment-rm/prompt-empty-stdin-skip",
		setup:         [][]string{{"touch", "inbox/cr", "-t", "CR"}, {"comment", "add", "inbox/cr", "body one"}},
		args:          []string{"comment", "rm", "C-00001"},
		stdin:         []byte(""),
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// Unknown comment: warn-and-continue (NOT fatal), empty result array, exit 0.
		name:    "comment-rm/unknown-warn-continue",
		setup:   [][]string{{"touch", "inbox/cr", "-t", "CR"}},
		args:    []string{"comment", "rm", "C-09999", "--yes"},
		mutates: true,
	},
	{
		// Invalid ref SHAPE: hard error, aborts the whole loop.
		name:    "comment-rm/invalid-ref-errors",
		setup:   [][]string{{"touch", "inbox/cr", "-t", "CR"}},
		args:    []string{"comment", "rm", "not-a-ref", "--yes"},
		mutates: true,
	},
	{
		// --if-match MISMATCH: warn + skip, no mutation, empty result array.
		name:          "comment-rm/if-match-mismatch-skip",
		setup:         [][]string{{"touch", "inbox/cr", "-t", "CR"}, {"comment", "add", "inbox/cr", "body one"}},
		args:          []string{"comment", "rm", "C-00001", "--if-match", "999", "--yes"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// --if-match MATCH (fresh comment etag=1): delete proceeds.
		name:          "comment-rm/if-match-match",
		setup:         [][]string{{"touch", "inbox/cr", "-t", "CR"}, {"comment", "add", "inbox/cr", "body one"}},
		args:          []string{"comment", "rm", "C-00001", "--if-match", "1", "--yes"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// Dry-run (non-TTY): emits the {id, task_id, action, dry_run} JSON array, no mutation.
		name:          "comment-rm/dry-run-json",
		setup:         [][]string{{"touch", "inbox/cr", "-t", "CR"}, {"comment", "add", "inbox/cr", "body one"}},
		args:          []string{"comment", "rm", "C-00001", "--dry-run"},
		normalizeUUID: true,
	},
	{
		// Multi-arg with --yes: both soft-deleted, ordered result array.
		name: "comment-rm/multi-yes",
		setup: [][]string{
			{"touch", "inbox/cr", "-t", "CR"},
			{"comment", "add", "inbox/cr", "body one"},
			{"comment", "add", "inbox/cr", "body two"},
		},
		args:          []string{"comment", "rm", "C-00001", "C-00002", "--yes"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// c: typed-selector prefix is accepted (stripped before resolve).
		name:          "comment-rm/c-prefix-yes",
		setup:         [][]string{{"touch", "inbox/cr", "-t", "CR"}, {"comment", "add", "inbox/cr", "body one"}},
		args:          []string{"comment", "rm", "c:C-00001", "--yes"},
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
	// container cat render modes (CLI-side on the wrkq.container.catView projection).
	// ndjson + porcelain are compact single-line JSON of the same object; --json and
	// the non-TTY default are indented. --no-frontmatter is the "raw" body-only mode
	// (markdown path with the front matter suppressed → description only, empty for
	// mkdir-seeded containers). Markdown WITH front matter only renders on a TTY and
	// so cannot be byte-parity tested under the pipe-based harness.
	{
		name:  "container-cat/ndjson",
		setup: [][]string{{"mkdir", "myproj"}},
		args:  []string{"container", "cat", "myproj", "--ndjson"},
	},
	{
		name:  "container-cat/porcelain",
		setup: [][]string{{"mkdir", "myproj"}},
		args:  []string{"container", "cat", "myproj", "--porcelain"},
	},
	{
		name:  "container-cat/non-tty-default-json",
		setup: [][]string{{"mkdir", "myproj"}},
		args:  []string{"container", "cat", "myproj"},
	},
	{
		name:  "container-cat/no-frontmatter-raw",
		setup: [][]string{{"mkdir", "myproj"}},
		args:  []string{"container", "cat", "myproj", "--no-frontmatter"},
	},
	{
		// A container with webhook_urls set exercises the webhook_urls array in the
		// json/ndjson/porcelain projections (and the front-matter line on a TTY).
		name:  "container-cat/webhooks-ndjson",
		setup: [][]string{{"mkdir", "hooked"}, {"container", "set", "hooked", "--webhook-urls", `["https://example.test/a","https://example.test/b"]`}},
		args:  []string{"container", "cat", "hooked", "--ndjson"},
	},
	{
		name:  "container-cat/webhooks-json",
		setup: [][]string{{"mkdir", "hooked"}, {"container", "set", "hooked", "--webhook-urls", `["https://example.test/a"]`}},
		args:  []string{"container", "cat", "hooked", "--json"},
	},
	{
		name:  "container-cat/unknown-ref-errors",
		setup: nil,
		args:  []string{"container", "cat", "P-09999999", "--json"},
	},
	// container set via wrkq.container.webhookSet (dedicated per-container webhook
	// mutation, deliberately separate from narrow wrkq.container.update).
	{
		name:    "container-set/webhook-urls-json",
		setup:   [][]string{{"mkdir", "hooked"}},
		args:    []string{"container", "set", "hooked", "--webhook-urls", `["https://example.test/a","https://example.test/b"]`},
		mutates: true,
	},
	{
		name:    "container-set/webhook-url-repeat",
		setup:   [][]string{{"mkdir", "hooked"}},
		args:    []string{"container", "set", "hooked", "--webhook-url", "https://example.test/a", "--webhook-url", "https://example.test/b"},
		mutates: true,
	},
	{
		name:    "container-set/add-remove",
		setup:   [][]string{{"mkdir", "hooked"}, {"container", "set", "hooked", "--webhook-urls", `["https://example.test/old","https://example.test/keep"]`}},
		args:    []string{"container", "set", "hooked", "--add-webhook-url", "https://example.test/new", "--remove-webhook-url", "https://example.test/old"},
		mutates: true,
	},
	{
		name:    "container-set/all-add",
		setup:   [][]string{{"mkdir", "alpha"}, {"mkdir", "beta"}},
		args:    []string{"container", "set", "--all", "--add-webhook-url", "https://example.test/all"},
		mutates: true,
	},
	{
		name:  "container-set/no-updates-errors",
		setup: [][]string{{"mkdir", "hooked"}},
		args:  []string{"container", "set", "hooked"},
	},
	{
		name: "container-set/all-without-delta-errors",
		args: []string{"container", "set", "--all"},
	},
	{
		name:  "container-set/all-with-arg-errors",
		setup: [][]string{{"mkdir", "hooked"}},
		args:  []string{"container", "set", "hooked", "--all", "--add-webhook-url", "https://example.test/all"},
	},
	{
		name:  "container-set/invalid-json-errors",
		setup: [][]string{{"mkdir", "hooked"}},
		args:  []string{"container", "set", "hooked", "--webhook-urls", `["unterminated"`},
	},
	{
		name:  "container-set/invalid-url-errors",
		setup: [][]string{{"mkdir", "hooked"}},
		args:  []string{"container", "set", "hooked", "--webhook-url", "ftp://example.test/hook"},
	},
	{
		name: "container-set/missing-container-errors",
		args: []string{"container", "set", "missing", "--webhook-url", "https://example.test/a"},
	},
	{
		name:  "comment-cat/by-id",
		setup: [][]string{{"touch", "inbox/cc", "-t", "CC"}, {"comment", "add", "inbox/cc", "the body"}},
		args:  []string{"comment", "cat", "C-00001", "--json"},
	},
	{
		// non-TTY default == JSON (no flag); proves the implicit-json path.
		name:  "comment-cat/default-nontty-json",
		setup: [][]string{{"touch", "inbox/cc", "-t", "CC"}, {"comment", "add", "inbox/cc", "the body"}},
		args:  []string{"comment", "cat", "C-00001"},
	},
	{
		// c: typed selector echoes the stripped ref in JSON but the original in errors.
		name:  "comment-cat/by-c-token",
		setup: [][]string{{"touch", "inbox/cc", "-t", "CC"}, {"comment", "add", "inbox/cc", "the body"}},
		args:  []string{"comment", "cat", "c:C-00001", "--json"},
	},
	{
		name:  "comment-cat/ndjson",
		setup: [][]string{{"touch", "inbox/cc", "-t", "CC"}, {"comment", "add", "inbox/cc", "the body"}},
		args:  []string{"comment", "cat", "C-00001", "--ndjson"},
	},
	{
		name: "comment-cat/ndjson-multi",
		setup: [][]string{
			{"touch", "inbox/cc", "-t", "CC"},
			{"comment", "add", "inbox/cc", "one"},
			{"comment", "add", "inbox/cc", "two Ã¢ÂÂ"},
		},
		args: []string{"comment", "cat", "C-00001", "C-00002", "--ndjson"},
	},
	{
		name:  "comment-cat/raw-single",
		setup: [][]string{{"touch", "inbox/cc", "-t", "CC"}, {"comment", "add", "inbox/cc", "raw body line\nsecond line"}},
		args:  []string{"comment", "cat", "C-00001", "--raw"},
	},
	{
		name: "comment-cat/raw-multi",
		setup: [][]string{
			{"touch", "inbox/cc", "-t", "CC"},
			{"comment", "add", "inbox/cc", "first"},
			{"comment", "add", "inbox/cc", "second"},
		},
		args: []string{"comment", "cat", "C-00001", "C-00002", "--raw"},
	},
	{
		name:  "comment-cat/not-found-errors",
		setup: nil,
		args:  []string{"comment", "cat", "C-09999", "--json"},
	},
	{
		// not-found preserves the original c: prefix in the error message.
		name:  "comment-cat/not-found-c-token-errors",
		setup: nil,
		args:  []string{"comment", "cat", "c:C-09999"},
	},
	{
		name:  "comment-cat/invalid-ref-errors",
		setup: nil,
		args:  []string{"comment", "cat", "not-a-ref", "--json"},
	},
	{
		// invalid ref preserves the original c: prefix in the error message.
		name:  "comment-cat/invalid-ref-c-token-errors",
		setup: nil,
		args:  []string{"comment", "cat", "c:bogus", "--json"},
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
	{
		name:  "comment-ls/yaml",
		setup: [][]string{{"touch", "inbox/cl", "-t", "CL"}, {"comment", "add", "inbox/cl", "one"}, {"comment", "add", "inbox/cl", "two"}},
		args:  []string{"comment", "ls", "inbox/cl", "--yaml"},
	},
	{
		name:  "comment-ls/tsv",
		setup: [][]string{{"touch", "inbox/cl", "-t", "CL"}, {"comment", "add", "inbox/cl", "one"}, {"comment", "add", "inbox/cl", "two"}},
		args:  []string{"comment", "ls", "inbox/cl", "--tsv"},
	},
	{
		name:  "comment-ls/reverse-json",
		setup: [][]string{{"touch", "inbox/cl", "-t", "CL"}, {"comment", "add", "inbox/cl", "one"}, {"comment", "add", "inbox/cl", "two"}, {"comment", "add", "inbox/cl", "three"}},
		args:  []string{"comment", "ls", "inbox/cl", "--json", "--reverse"},
	},
	{
		name:  "comment-ls/reverse-sort-id",
		setup: [][]string{{"touch", "inbox/cl", "-t", "CL"}, {"comment", "add", "inbox/cl", "one"}, {"comment", "add", "inbox/cl", "two"}},
		args:  []string{"comment", "ls", "inbox/cl", "--json", "--sort", "id", "--reverse"},
	},
	{
		name: "comment-ls/multi-task-json",
		setup: [][]string{
			{"touch", "inbox/ca", "-t", "CA"}, {"comment", "add", "inbox/ca", "a-one"},
			{"touch", "inbox/cb", "-t", "CB"}, {"comment", "add", "inbox/cb", "b-one"}, {"comment", "add", "inbox/cb", "b-two"},
		},
		args: []string{"comment", "ls", "inbox/ca", "inbox/cb", "--json"},
	},
	{
		name: "comment-ls/multi-task-ndjson-default",
		setup: [][]string{
			{"touch", "inbox/ca", "-t", "CA"}, {"comment", "add", "inbox/ca", "a-one"},
			{"touch", "inbox/cb", "-t", "CB"}, {"comment", "add", "inbox/cb", "b-one"},
		},
		args: []string{"comment", "ls", "inbox/ca", "inbox/cb"},
	},
	{
		name: "comment-ls/multi-task-yaml",
		setup: [][]string{
			{"touch", "inbox/ca", "-t", "CA"}, {"comment", "add", "inbox/ca", "a-one"},
			{"touch", "inbox/cb", "-t", "CB"}, {"comment", "add", "inbox/cb", "b-one"},
		},
		args: []string{"comment", "ls", "inbox/ca", "inbox/cb", "--yaml"},
	},
	{
		name: "comment-ls/multi-task-tsv",
		setup: [][]string{
			{"touch", "inbox/ca", "-t", "CA"}, {"comment", "add", "inbox/ca", "a-one"},
			{"touch", "inbox/cb", "-t", "CB"}, {"comment", "add", "inbox/cb", "b-one"},
		},
		args: []string{"comment", "ls", "inbox/ca", "inbox/cb", "--tsv"},
	},
	{
		name: "comment-ls/multi-task-porcelain-limit",
		setup: [][]string{
			{"touch", "inbox/ca", "-t", "CA"}, {"comment", "add", "inbox/ca", "a-one"}, {"comment", "add", "inbox/ca", "a-two"},
			{"touch", "inbox/cb", "-t", "CB"}, {"comment", "add", "inbox/cb", "b-one"}, {"comment", "add", "inbox/cb", "b-two"},
		},
		args: []string{"comment", "ls", "inbox/ca", "inbox/cb", "--porcelain", "--limit", "3"},
	},

	// attach ls via wrkq.attachment.listView (server compat list projection,
	// DB-only, cursor-paginated). Populated rows are seeded with the legacy binary's
	// `attach put` (source files materialized into the fixture dir before seeding).
	{
		name:  "attach-ls/empty",
		setup: [][]string{{"touch", "inbox/at", "-t", "AT"}},
		args:  []string{"attach", "ls", "inbox/at", "--json"},
	},
	{
		name: "attach-ls/populated-json",
		setup: [][]string{
			{"touch", "inbox/at", "-t", "AT"},
			{"attach", "put", "inbox/at", "alpha.txt"},
			{"attach", "put", "inbox/at", "beta.md"},
		},
		args:  []string{"attach", "ls", "inbox/at", "--json"},
		files: attachSrcFiles,
	},
	{
		name: "attach-ls/populated-ndjson-default",
		setup: [][]string{
			{"touch", "inbox/at", "-t", "AT"},
			{"attach", "put", "inbox/at", "alpha.txt"},
			{"attach", "put", "inbox/at", "beta.md"},
		},
		args:  []string{"attach", "ls", "inbox/at"},
		files: attachSrcFiles,
	},
	{
		name: "attach-ls/populated-porcelain-limit-cursor",
		setup: [][]string{
			{"touch", "inbox/at", "-t", "AT"},
			{"attach", "put", "inbox/at", "alpha.txt"},
			{"attach", "put", "inbox/at", "beta.md"},
			{"attach", "put", "inbox/at", "gamma.bin"},
		},
		args:  []string{"attach", "ls", "inbox/at", "--porcelain", "--limit", "2"},
		files: attachSrcFiles,
	},

	// attach put via wrkq.attachment.add (server reads the host file bytes and
	// writes them into the server-local attach dir). WRKQ_ATTACH_DIR is set
	// relative so each binary writes into its OWN run dir (cmd.Dir), keeping them
	// isolated despite identical task UUIDs in the copied DB.
	{
		name:          "attach-put/basic",
		setup:         [][]string{{"touch", "inbox/at", "-t", "AT"}},
		args:          []string{"attach", "put", "inbox/at", "alpha.txt"},
		files:         attachSrcFiles,
		env:           []string{"WRKQ_ATTACH_DIR=attach-store"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		name:          "attach-put/with-name-and-mime",
		setup:         [][]string{{"touch", "inbox/at", "-t", "AT"}},
		args:          []string{"attach", "put", "inbox/at", "alpha.txt", "--name", "renamed.dat", "--mime", "application/x-custom"},
		files:         attachSrcFiles,
		env:           []string{"WRKQ_ATTACH_DIR=attach-store"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		name:    "attach-put/unknown-task-errors",
		setup:   nil,
		args:    []string{"attach", "put", "T-09999999", "alpha.txt"},
		files:   attachSrcFiles,
		env:     []string{"WRKQ_ATTACH_DIR=attach-store"},
		mutates: false,
	},

	// attach rm via wrkq.attachment.remove (metadata DELETE + server-side unlink).
	// --yes skips the interactive confirmation so the harness (empty stdin) is
	// deterministic. Attachment friendly ids are ATT-NNNNN (attachment_seq), so the
	// first seeded attachment is the deterministic ATT-00001.
	{
		name: "attach-rm/yes",
		setup: [][]string{
			{"touch", "inbox/at", "-t", "AT"},
			{"attach", "put", "inbox/at", "alpha.txt"},
		},
		args:    []string{"attach", "rm", "ATT-00001", "--yes"},
		files:   attachSrcFiles,
		env:     []string{"WRKQ_ATTACH_DIR=attach-store"},
		mutates: true,
	},
	{
		name: "attach-rm/unknown-warns-continues",
		setup: [][]string{
			{"touch", "inbox/at", "-t", "AT"},
			{"attach", "put", "inbox/at", "alpha.txt"},
		},
		args:    []string{"attach", "rm", "ATT-09999999", "--yes"},
		files:   attachSrcFiles,
		env:     []string{"WRKQ_ATTACH_DIR=attach-store"},
		mutates: true,
	},

	// attach get via wrkq.attachment.getBytes (byte transfer: server returns base64
	// chunks + size/checksum; the MIRROR decodes and writes raw bytes to stdout /
	// --as). The seed `attach put` writes the bytes into the seeding binary's
	// HOME-default attach dir (shared HOME); the get runs WITHOUT WRKQ_ATTACH_DIR so
	// both binaries read that same dir. relative_path (in the copied DB) is
	// dir-independent. Attachment friendly ids are deterministic ATT-NNNNN.
	{
		// `--as -` forces stdout (the harness's global `--as local-human` would
		// otherwise shadow the local output-path flag → file mode; legacy has the
		// same collision, so `--as -` is the deterministic way to exercise stdout).
		name: "attach-get/stdout-text",
		setup: [][]string{
			{"touch", "inbox/at", "-t", "AT"},
			{"attach", "put", "inbox/at", "alpha.txt"},
		},
		args:              []string{"attach", "get", "ATT-00001", "--as", "-"},
		files:             attachSrcFiles,
		seededAttachStore: true,
	},
	{
		// gamma.bin carries NUL + control bytes — the raw stdout must survive the
		// base64 round-trip byte-for-byte (binary payload guard).
		name: "attach-get/stdout-binary-nul",
		setup: [][]string{
			{"touch", "inbox/at", "-t", "AT"},
			{"attach", "put", "inbox/at", "gamma.bin"},
		},
		args:              []string{"attach", "get", "ATT-00001", "--as", "-"},
		files:             attachSrcFiles,
		seededAttachStore: true,
	},
	{
		// --as <path>: the mirror writes the bytes to a local file (CLI-owned) and
		// prints the JSON {copied:true,path:...} result. The JSON result is
		// byte-matched.
		name: "attach-get/as-path-json",
		setup: [][]string{
			{"touch", "inbox/at", "-t", "AT"},
			{"attach", "put", "inbox/at", "beta.md"},
		},
		args:              []string{"attach", "get", "ATT-00001", "--as", "out.copy"},
		files:             attachSrcFiles,
		seededAttachStore: true,
	},
	{
		name:              "attach-get/unknown-attachment-errors",
		setup:             [][]string{{"touch", "inbox/at", "-t", "AT"}},
		args:              []string{"attach", "get", "ATT-09999999", "--as", "-"},
		files:             attachSrcFiles,
		seededAttachStore: true,
	},

	// attach put - via wrkq.attachment.addBytes (byte UPLOAD: the MIRROR reads stdin
	// and sends base64 chunks; the server stages + commits). --name is required for
	// stdin. WRKQ_ATTACH_DIR is per-run so each binary stages into its own cmd.Dir.
	{
		name:          "attach-put-stdin/basic",
		setup:         [][]string{{"touch", "inbox/at", "-t", "AT"}},
		args:          []string{"attach", "put", "inbox/at", "-", "--name", "piped.txt"},
		stdin:         []byte("piped attachment body\n"),
		env:           []string{"WRKQ_ATTACH_DIR=attach-store"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// binary stdin with NUL/newlines + an explicit --mime override.
		name:          "attach-put-stdin/binary-with-mime",
		setup:         [][]string{{"touch", "inbox/at", "-t", "AT"}},
		args:          []string{"attach", "put", "inbox/at", "-", "--name", "blob.dat", "--mime", "application/x-custom"},
		stdin:         []byte("blob\x00\x01\x02\nmore\nbytes"),
		env:           []string{"WRKQ_ATTACH_DIR=attach-store"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// --name required for stdin: legacy errors "--name is required when reading
		// from stdin" before touching anything; the mirror must match.
		name:    "attach-put-stdin/name-required-errors",
		setup:   [][]string{{"touch", "inbox/at", "-t", "AT"}},
		args:    []string{"attach", "put", "inbox/at", "-"},
		stdin:   []byte("ignored"),
		env:     []string{"WRKQ_ATTACH_DIR=attach-store"},
		mutates: false,
	},
	{
		// duplicate filename for the task → conflict (legacy errors before write).
		name: "attach-put-stdin/duplicate-filename-errors",
		setup: [][]string{
			{"touch", "inbox/at", "-t", "AT"},
			{"attach", "put", "inbox/at", "alpha.txt"},
		},
		args:    []string{"attach", "put", "inbox/at", "-", "--name", "alpha.txt"},
		stdin:   []byte("dup body"),
		files:   attachSrcFiles,
		env:     []string{"WRKQ_ATTACH_DIR=attach-store"},
		mutates: false,
	},
	{
		// unknown task → resolve error, no write.
		name:    "attach-put-stdin/unknown-task-errors",
		setup:   nil,
		args:    []string{"attach", "put", "T-09999999", "-", "--name", "x.txt"},
		stdin:   []byte("body"),
		env:     []string{"WRKQ_ATTACH_DIR=attach-store"},
		mutates: false,
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
	// ── UNGATED render modes now byte-proven against legacy (E2) ──
	// The typed paths decode the byte-proven findListView projection back into
	// the legacy findResult struct so internal/render output is byte-identical
	// (yaml.v3 keys off the Go field names). Mirrors the ls ungate.
	{
		// --print0: NUL-joined Path values + trailing NUL; precedence over mode.
		name:  "find/print0",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--print0"},
	},
	{
		name:  "find/print0-empty",
		setup: findMixed,
		args:  []string{"find", "nope", "--print0"},
	},
	{
		name:  "find/output-table",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--output", "table"},
	},
	{
		name:  "find/pretty",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--pretty"},
	},
	{
		// human has no legacy render branch -> table fall-through (identical bytes).
		name:  "find/output-human",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--output", "human"},
	},
	{
		name:  "find/output-yaml",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--output", "yaml"},
	},
	{
		name:  "find/output-tsv",
		setup: findMixed,
		args:  []string{"find", "proj", "--type", "t", "--output", "tsv"},
	},
	{
		// Mixed-type yaml exercises containers (nil state/priority -> null in yaml).
		name:  "find/output-yaml-mixed",
		setup: findMixed,
		args:  []string{"find", "proj", "--output", "yaml"},
	},
	{
		// Both binaries reject two output flags with the identical message.
		name:  "find/conflicting-modes",
		setup: findMixed,
		args:  []string{"find", "proj", "--json", "--ndjson"},
	},

	// log via wrkq.history.listView (server-owned compat history read model over the
	// generic event_log table — distinct from wrkf.event.query/workflow_events). The
	// server resolves the caller-scoped target (task/container/actor by friendly ID
	// T-*/P-*/A- OR UUID), filters since/until, and owns cursor.Apply + limit+1 +
	// BuildNextCursor over event_log.id DESC. logSeed creates a task (one create
	// event), a container, and a relation so task + container histories are non-empty.
	//
	// Byte-proven modes: --json (incl. empty → null), NDJSON (non-TTY default +
	// --porcelain), the porcelain next_cursor→stderr contract, --since/--until,
	// malformed cursor, and every resolution error path. The oneline/detailed/--patch
	// presentation modes render their Summary/Changes lines by iterating the decoded
	// payload MAP, which Go randomizes — legacy itself is non-byte-stable for any
	// multi-key payload (proven: two legacy --patch runs differ), so those modes
	// CANNOT have a byte-parity fixture. The mirror copies legacy's render code
	// verbatim (faithful, not narrower); the divergence is documented in
	// docs/rpc-cli-migration.md, NOT silently degraded and NOT hard-gated (legacy
	// succeeds, so a gate would be worse parity).
	{
		name:  "log/task-id-json",
		setup: logSeed,
		args:  []string{"log", "T-00001", "--json"},
	},
	{
		name:  "log/task-id-ndjson-default",
		setup: logSeed,
		args:  []string{"log", "T-00001"},
	},
	{
		name:  "log/container-id-json",
		setup: logSeed,
		args:  []string{"log", "P-00002", "--json"},
	},
	{
		name:  "log/porcelain-limit-next-cursor",
		setup: logSeedMultiEvent,
		args:  []string{"log", "P-00002", "--porcelain", "--limit", "1"},
	},
	{
		name:  "log/since-excludes-all",
		setup: logSeed,
		args:  []string{"log", "T-00001", "--json", "--since", "2099-01-01"},
	},
	{
		name:  "log/until-excludes-all",
		setup: logSeed,
		args:  []string{"log", "T-00001", "--json", "--until", "2000-01-01"},
	},
	{
		name:  "log/since-includes-all",
		setup: logSeed,
		args:  []string{"log", "T-00001", "--json", "--since", "2000-01-01"},
	},
	{
		name:  "log/since-invalid-errors",
		setup: logSeed,
		args:  []string{"log", "T-00001", "--json", "--since", "not-a-date"},
	},
	{
		name:  "log/until-invalid-errors",
		setup: logSeed,
		args:  []string{"log", "T-00001", "--json", "--until", "not-a-date"},
	},
	{
		name:  "log/malformed-cursor-errors",
		setup: logSeed,
		args:  []string{"log", "T-00001", "--json", "--cursor", "not-a-valid-cursor"},
	},
	{
		name:  "log/unknown-task-errors",
		setup: logSeed,
		args:  []string{"log", "T-09999999", "--json"},
	},
	{
		name:  "log/unknown-container-errors",
		setup: logSeed,
		args:  []string{"log", "P-09999999", "--json"},
	},
	{
		name:  "log/unknown-actor-errors",
		setup: logSeed,
		args:  []string{"log", "A-09999999", "--json"},
	},
	{
		name:  "log/unknown-uuid-errors",
		setup: logSeed,
		args:  []string{"log", "00000000-0000-0000-0000-000000000000", "--json"},
	},
	{
		// PINNED DIVERGENCE: legacy advertises path targets in help but errors today
		// (path resolution is a TODO). Both binaries error identically.
		name:  "log/path-target-todo-errors",
		setup: logSeed,
		args:  []string{"log", "inbox/log-task", "--json"},
	},
	{
		// Project-root parity: a friendly ID is NOT prefixed by the root (it resolves
		// the same as without a root), so the history is identical to the root-less run.
		name:  "log/pr-id-not-prefixed",
		setup: logSeed,
		args:  []string{"log", "T-00001", "--json"},
		env:   []string{"WRKQ_PROJECT_ROOT=inbox"},
	},
	{
		// Project-root parity: a relative/path target IS scoped to <root>/<path>, then
		// still errors with the legacy scoped path-resolution-TODO message. Proves the
		// mirror scopes the raw target BEFORE the call (caller semantics).
		name:  "log/pr-relative-path-scoped-error",
		setup: logSeed,
		args:  []string{"log", "deep/thing", "--json"},
		env:   []string{"WRKQ_PROJECT_ROOT=inbox"},
	},

	// tree via wrkq.task.treeView (server-owned compat tree projection: recursive
	// traversal, container pruning, "all done" rollups, subtask nesting, hidden
	// counting). Forced --pretty pins WRKQ_NOW so "opened N ago" text is
	// deterministic; color remains off in the non-TTY harness.
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
		name:  "tree/multi-path-ignores-extra",
		setup: treeMixed,
		args:  []string{"tree", "proj", "inbox", "--json"},
	},
	{
		name:  "tree/fields-ignored",
		setup: treeMixed,
		args:  []string{"tree", "proj", "--json", "--fields", "id,slug"},
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
		name:  "tree/pretty",
		setup: treeMixed,
		args:  []string{"tree", "proj", "--pretty"},
		env:   []string{"WRKQ_NOW=2026-06-25T12:00:00Z"},
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
	{
		name:  "tree/output-table",
		setup: treeMixed,
		args:  []string{"tree", "proj", "--output", "table"},
		env:   []string{"WRKQ_NOW=2026-06-25T12:00:00Z"},
	},
	{
		name:  "tree/output-human",
		setup: treeMixed,
		args:  []string{"tree", "proj", "--output", "human"},
		env:   []string{"WRKQ_NOW=2026-06-25T12:00:00Z"},
	},
	{
		name:  "tree/output-yaml-unsupported",
		setup: treeMixed,
		args:  []string{"tree", "proj", "--output", "yaml"},
	},
	{
		name:  "tree/output-tsv-unsupported",
		setup: treeMixed,
		args:  []string{"tree", "proj", "--output", "tsv"},
	},
	{
		name:  "tree/conflicting-modes-errors",
		setup: treeMixed,
		args:  []string{"tree", "proj", "--json", "--ndjson"},
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
		name:  "ls/pretty",
		setup: lsMixed,
		args:  []string{"ls", "proj", "--pretty"},
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

	// apply ✓ wrkq.task.update via the caller-side parse/gate. The mirror reads the
	// file/stdin, parses (md/yaml/json), gates metadata, prechecks etag, and sends a
	// single update patch. Output shape (uuid/updated/fields snake_case) is mirror-owned.
	{
		// Plain markdown file → description-only body update.
		name:    "apply/md-file-description",
		setup:   [][]string{{"touch", "inbox/doc", "-t", "Doc"}},
		args:    []string{"apply", "inbox/doc", "body.md"},
		files:   map[string]string{"body.md": "This is the new description\n\nwith paragraphs.\n"},
		mutates: true,
	},
	{
		// stdin (-) description update; the mirror reads cmd stdin, never server stdin.
		name:    "apply/stdin-description",
		setup:   [][]string{{"touch", "inbox/doc", "-t", "Doc"}},
		args:    []string{"apply", "inbox/doc", "-"},
		stdin:   []byte("piped description body\n"),
		mutates: true,
	},
	{
		// YAML front matter carrying a specification (body-only allows specification).
		name:    "apply/md-frontmatter-spec",
		setup:   [][]string{{"touch", "inbox/doc", "-t", "Doc"}},
		args:    []string{"apply", "inbox/doc", "spec.md"},
		files:   map[string]string{"spec.md": "---\nspecification: |\n  # Spec heading\n  detail\n---\nThe description body\n"},
		mutates: true,
	},
	{
		// Metadata present without --with-metadata: warning to stderr + fields dropped,
		// only the description survives. Proves the gate + warning byte-match.
		name:    "apply/metadata-gated-warn",
		setup:   [][]string{{"touch", "inbox/doc", "-t", "Doc"}},
		args:    []string{"apply", "inbox/doc", "meta.md"},
		files:   map[string]string{"meta.md": "---\ntitle: Ignored Title\nstate: in_progress\n---\nDescription survives\n"},
		mutates: true,
	},
	{
		// --with-metadata applies title/state/priority/due_at alongside the body.
		name:    "apply/with-metadata",
		setup:   [][]string{{"touch", "inbox/doc", "-t", "Doc"}},
		args:    []string{"apply", "inbox/doc", "full.md", "--with-metadata"},
		files:   map[string]string{"full.md": "---\ntitle: New Title\nstate: in_progress\npriority: 1\n---\nFull body\n"},
		mutates: true,
	},
	{
		// dry-run: non-TTY JSON plan, no mutation.
		name:  "apply/dry-run-json",
		setup: [][]string{{"touch", "inbox/doc", "-t", "Doc"}},
		args:  []string{"apply", "inbox/doc", "body.md", "--dry-run"},
		files: map[string]string{"body.md": "Planned description\n"},
	},
	{
		// etag mismatch precheck owned by the CLI (distinct error text), no mutation.
		name:  "apply/if-match-mismatch-errors",
		setup: [][]string{{"touch", "inbox/doc", "-t", "Doc"}},
		args:  []string{"apply", "inbox/doc", "body.md", "--if-match", "999"},
		files: map[string]string{"body.md": "wont apply\n"},
	},
	{
		// empty input rejected before any mutation.
		name:  "apply/empty-input-errors",
		setup: [][]string{{"touch", "inbox/doc", "-t", "Doc"}},
		args:  []string{"apply", "inbox/doc", "-"},
		stdin: []byte(""),
	},
	{
		// unknown task → "failed to resolve task: task not found: <id>" (byte-match).
		name:  "apply/unknown-task-errors",
		setup: nil,
		args:  []string{"apply", "T-09999999", "-"},
		stdin: []byte("body"),
	},
	{
		// project-root scoping: the bare ref resolves under the configured root.
		name:    "apply/project-root",
		setup:   prSeed,
		args:    []string{"apply", "task-a", "body.md"},
		files:   map[string]string{"body.md": "scoped description\n"},
		env:     []string{"WRKQ_PROJECT_ROOT=myproj"},
		mutates: true,
	},

	// cp ✓ server-owned deep copy via wrkq.task.copy. The CLI owns fan-out, stdin
	// sources, the >5-source prompt (PTY-tested separately), dry-run, nullglob,
	// jobs/continue-on-error, output rendering, and project-root scoping.
	{
		// New copy into a different container: a fresh task row is created. The dest
		// uuid/id are freshly generated → normalize.
		name:          "cp/new-into-container",
		setup:         [][]string{{"touch", "inbox/orig", "-t", "Orig"}, {"mkdir", "dest"}},
		args:          []string{"cp", "inbox/orig", "dest"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// Legacy accepts -r/--recursive but still resolves only source tasks; for a
		// task source this is the same deep task copy as the default path.
		name:          "cp/recursive-task-source",
		setup:         [][]string{{"touch", "inbox/orig", "-t", "Orig"}, {"mkdir", "dest"}},
		args:          []string{"cp", "inbox/orig", "dest", "--recursive"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// Container recursion is not implemented in legacy either: the source still
		// goes through task resolution and therefore errors as "task not found".
		name:  "cp/recursive-container-source-errors",
		setup: [][]string{{"mkdir", "inbox/src"}, {"touch", "inbox/src/aa", "-t", "A"}, {"mkdir", "dest"}},
		args:  []string{"cp", "inbox/src", "dest", "--recursive"},
	},
	{
		// --overwrite onto an existing slug updates the existing row (deterministic
		// dest uuid → no normalization, so a wrong-uuid bug is still caught).
		name: "cp/overwrite-existing",
		setup: [][]string{
			{"touch", "inbox/orig", "-t", "Orig", "--priority", "1"},
			{"mkdir", "dest"}, {"touch", "dest/orig", "-t", "Stale"},
		},
		args:    []string{"cp", "inbox/orig", "dest", "--overwrite"},
		mutates: true,
	},
	{
		// Dest slug-conflict WITHOUT --overwrite errors (no durable change).
		name: "cp/slug-conflict-no-overwrite-errors",
		setup: [][]string{
			{"touch", "inbox/orig", "-t", "Orig"},
			{"mkdir", "dest"}, {"touch", "dest/orig", "-t", "Existing"},
		},
		args:    []string{"cp", "inbox/orig", "dest"},
		mutates: true,
	},
	{
		// --shallow copies neither metadata nor files (attachments_copied omitted).
		name:          "cp/shallow",
		setup:         [][]string{{"touch", "inbox/orig", "-t", "Orig"}, {"mkdir", "dest"}},
		args:          []string{"cp", "inbox/orig", "dest", "--shallow"},
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// Mutually-exclusive flag validation (CLI-owned, before any RPC).
		name:  "cp/with-attachments-and-shallow-errors",
		setup: [][]string{{"touch", "inbox/orig", "-t", "Orig"}, {"mkdir", "dest"}},
		args:  []string{"cp", "inbox/orig", "dest", "--with-attachments", "--shallow"},
	},
	{
		// Unknown source errors (NOT_FOUND taxonomy, no nullglob).
		name:  "cp/unknown-source-errors",
		setup: [][]string{{"mkdir", "dest"}},
		args:  []string{"cp", "T-09999999", "dest"},
	},
	{
		// Unknown destination container errors.
		name:  "cp/unknown-dest-errors",
		setup: [][]string{{"touch", "inbox/orig", "-t", "Orig"}},
		args:  []string{"cp", "inbox/orig", "nope/missing"},
	},
	{
		// nullglob: a missing source is a silent success (no error, no copy).
		name:    "cp/nullglob-missing-source",
		setup:   [][]string{{"mkdir", "dest"}},
		args:    []string{"cp", "T-09999999", "dest", "--nullglob"},
		mutates: true,
	},
	{
		// stdin sources: one selector per line, fanned out by the CLI.
		name: "cp/stdin-sources",
		setup: [][]string{
			{"touch", "inbox/aa", "-t", "A"}, {"touch", "inbox/bb", "-t", "B"}, {"mkdir", "dest"},
		},
		args:          []string{"cp", "-", "dest"},
		stdin:         []byte("inbox/aa\ninbox/bb\n"),
		mutates:       true,
		normalizeUUID: true,
	},
	{
		// dry-run renders the JSON plan (non-TTY) without mutating.
		name:    "cp/dry-run-json",
		setup:   [][]string{{"touch", "inbox/orig", "-t", "Orig"}, {"mkdir", "dest"}},
		args:    []string{"cp", "inbox/orig", "dest", "--dry-run"},
		mutates: true,
	},
	{
		// with-attachments: real file copy into the SAME store + metadata cascade.
		// Seeded shared attach store so the source bytes are reachable at copy time.
		name: "cp/with-attachments",
		setup: [][]string{
			{"touch", "inbox/orig", "-t", "Orig"}, {"mkdir", "dest"},
			{"attach", "put", "inbox/orig", "att.txt"},
		},
		args:              []string{"cp", "inbox/orig", "dest", "--with-attachments"},
		files:             map[string]string{"att.txt": "attachment payload\n"},
		mutates:           true,
		normalizeUUID:     true,
		seededAttachStore: true,
	},
	{
		// project-root scoping: bare source/dest refs resolve under the root.
		name:          "cp/project-root",
		setup:         prSeed,
		args:          []string{"cp", "task-a", "sub"},
		env:           []string{"WRKQ_PROJECT_ROOT=myproj"},
		mutates:       true,
		normalizeUUID: true,
	},

	// ─── handoff family (T-05117) ───────────────────────────────────────────────
	// Handoff scope is CALLER-owned: the seed + command share an ASP_SCOPE_REF env.
	// create echoes a fresh UUID + timestamps → normalizeUUID + <TS> normalization.
	{
		name:          "handoff/create-json",
		args:          []string{"handoff", "create", "-t", "Next steps", "--body-file", "body.md", "--json"},
		files:         map[string]string{"body.md": "carry-over notes\n"},
		env:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:       []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		normalizeUUID: true,
	},
	{
		name:          "handoff/create-dry-run",
		args:          []string{"handoff", "create", "-t", "Dry", "--body-file", "body.md", "--dry-run", "--json"},
		files:         map[string]string{"body.md": "dry body\n"},
		env:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:       []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		normalizeUUID: true,
	},
	{
		name:    "handoff/create-missing-title-errors",
		args:    []string{"handoff", "create", "--body-file", "body.md", "--json"},
		files:   map[string]string{"body.md": "body\n"},
		env:     []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv: []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
	},
	{
		name:          "handoff/get-json",
		setup:         [][]string{{"handoff", "create", "-t", "Findable", "--body-file", "body.md", "--json"}},
		args:          []string{"handoff", "get", "H-00001", "--json"},
		files:         map[string]string{"body.md": "findable body\n"},
		env:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:       []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		normalizeUUID: true,
	},
	{
		name:    "handoff/get-not-found-errors",
		args:    []string{"handoff", "get", "H-99999", "--json"},
		env:     []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv: []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
	},
	{
		name:          "handoff/list-ndjson",
		setup:         [][]string{{"handoff", "create", "-t", "Listed", "--body-file", "body.md", "--json"}},
		args:          []string{"handoff", "list", "--ndjson"},
		files:         map[string]string{"body.md": "listed body\n"},
		env:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:       []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		normalizeUUID: true,
	},
	{
		name:    "handoff/list-empty-json",
		args:    []string{"handoff", "list", "--json"},
		env:     []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv: []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
	},
	{
		name:          "handoff/acknowledge-json",
		setup:         [][]string{{"handoff", "create", "-t", "Ack me", "--body-file", "body.md", "--json"}},
		args:          []string{"--as", "agent:cody", "handoff", "acknowledge", "H-00001", "--note", "loaded next session", "--json"},
		files:         map[string]string{"body.md": "ack body\n"},
		env:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:       []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		normalizeUUID: true,
	},
	{
		name: "handoff/acknowledge-already-errors",
		setup: [][]string{
			{"handoff", "create", "-t", "Twice", "--body-file", "body.md", "--json"},
			{"--as", "agent:cody", "handoff", "acknowledge", "H-00001", "--json"},
		},
		args:          []string{"--as", "agent:cody", "handoff", "acknowledge", "H-00001", "--json"},
		files:         map[string]string{"body.md": "twice body\n"},
		env:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:       []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		normalizeUUID: true,
	},
	{
		name:          "handoff/acknowledge-dry-run",
		setup:         [][]string{{"handoff", "create", "-t", "Dry ack", "--body-file", "body.md", "--json"}},
		args:          []string{"--as", "agent:cody", "handoff", "acknowledge", "H-00001", "--dry-run", "--json"},
		files:         map[string]string{"body.md": "dry ack body\n"},
		env:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:       []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		normalizeUUID: true,
	},
	{
		name: "handoff/search-json",
		setup: [][]string{
			{"handoff", "create", "-t", "Quartz notes", "--body-file", "body.md", "--json"},
			{"handoff", "create", "-t", "Other topic", "--body-file", "other.md", "--json"},
			{"index", "rebuild"},
		},
		args:              []string{"handoff", "search", "quartz", "--json"},
		files:             map[string]string{"body.md": "carry quartz details\n", "other.md": "unrelated body\n"},
		env:               []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		searchIndex:       true,
		searchSeedRebuild: true,
		normalizeUUID:     true,
	},
	{
		name: "handoff/search-default-ndjson",
		setup: [][]string{
			{"handoff", "create", "-t", "Quartz notes", "--body-file", "body.md", "--json"},
			{"index", "rebuild"},
		},
		args:              []string{"handoff", "search", "quartz"},
		files:             map[string]string{"body.md": "carry quartz details\n"},
		env:               []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		searchIndex:       true,
		searchSeedRebuild: true,
		normalizeUUID:     true,
	},
	{
		name: "handoff/search-human",
		setup: [][]string{
			{"handoff", "create", "-t", "Quartz handoff", "--body-file", "body.md", "--json"},
			{"index", "rebuild"},
		},
		args:              []string{"handoff", "search", "quartz", "--human"},
		files:             map[string]string{"body.md": "carry quartz details\n"},
		env:               []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		searchIndex:       true,
		searchSeedRebuild: true,
	},
	{
		name: "handoff/search-human-empty",
		setup: [][]string{
			{"handoff", "create", "-t", "Other handoff", "--body-file", "body.md", "--json"},
			{"index", "rebuild"},
		},
		args:              []string{"handoff", "search", "missing", "--human"},
		files:             map[string]string{"body.md": "no match here\n"},
		env:               []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		searchIndex:       true,
		searchSeedRebuild: true,
	},
	{
		name: "handoff/search-acknowledged-status",
		setup: [][]string{
			{"handoff", "create", "-t", "Quartz pending", "--body-file", "body.md", "--json"},
			{"handoff", "create", "-t", "Quartz done", "--body-file", "done.md", "--json"},
			{"--as", "agent:cody", "handoff", "acknowledge", "H-00002", "--json"},
			{"index", "rebuild"},
		},
		args:              []string{"handoff", "search", "quartz", "--status", "acknowledged", "--json"},
		files:             map[string]string{"body.md": "pending quartz\n", "done.md": "done quartz\n"},
		env:               []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		searchIndex:       true,
		searchSeedRebuild: true,
		normalizeUUID:     true,
	},
	{
		name: "handoff/search-pagination",
		setup: [][]string{
			{"handoff", "create", "-t", "Quartz A", "--body-file", "body.md", "--json"},
			{"handoff", "create", "-t", "Quartz B", "--body-file", "body.md", "--json"},
			{"handoff", "create", "-t", "Quartz C", "--body-file", "body.md", "--json"},
			{"index", "rebuild"},
		},
		args:              []string{"handoff", "search", "quartz", "--limit", "2", "--porcelain"},
		files:             map[string]string{"body.md": "quartz payload\n"},
		env:               []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		searchIndex:       true,
		searchSeedRebuild: true,
		normalizeUUID:     true,
	},
	{
		name: "handoff/search-invalid-status",
		setup: [][]string{
			{"handoff", "create", "-t", "Quartz", "--body-file", "body.md", "--json"},
			{"index", "rebuild"},
		},
		args:              []string{"handoff", "search", "quartz", "--status", "bogus", "--json"},
		files:             map[string]string{"body.md": "quartz payload\n"},
		env:               []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		seedEnv:           []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
		searchIndex:       true,
		searchSeedRebuild: true,
	},
	{
		name:    "handoff/search-disabled-errors",
		setup:   [][]string{{"handoff", "create", "-t", "Quartz", "--body-file", "body.md", "--json"}},
		args:    []string{"handoff", "search", "quartz", "--json"},
		files:   map[string]string{"body.md": "quartz payload\n"},
		env:     []string{"ASP_SCOPE_REF=agent:cody:project:wrkq", "WRKQ_SEARCH_ENABLED=0"},
		seedEnv: []string{"ASP_SCOPE_REF=agent:cody:project:wrkq"},
	},
	// ── monitor (bounded polling via wrkq.monitor.eventsView + .stateView) ──────
	// monitor wait: condition already met → exactly one terminal line (result=met),
	// exit 0. The single stateView snapshot is authoritative; no events stream.
	{
		name:    "monitor/wait-already-met",
		setup:   [][]string{{"touch", "inbox/done", "-t", "done"}, {"set", "inbox/done", "--state", "completed"}},
		args:    []string{"monitor", "wait", "inbox/done", "--until", "state=completed"},
		mutates: false,
	},
	// monitor wait: condition not met within a short timeout → terminal result=timeout,
	// exit 1 + the "monitor wait ended: timeout" stderr. Bounded so the loop exits fast.
	{
		name:    "monitor/wait-timeout",
		setup:   [][]string{{"touch", "inbox/open", "-t", "open"}},
		args:    []string{"monitor", "wait", "inbox/open", "--until", "state=completed", "--timeout", "150ms"},
		mutates: false,
	},
	// monitor wait: missing --until → usage error (exit 2), reproduced caller-side.
	{
		name:    "monitor/wait-missing-until",
		setup:   [][]string{{"touch", "inbox/open", "-t", "open"}},
		args:    []string{"monitor", "wait", "inbox/open"},
		mutates: false,
	},
	// monitor wait: no task selectors → usage error (exit 2).
	{
		name:    "monitor/wait-no-selector",
		setup:   nil,
		args:    []string{"monitor", "wait", "--until", "state=completed"},
		mutates: false,
	},
	// monitor wait: invalid task selector → server-resolved validation error (exit 2)
	// with the legacy raw line + main's "Error:" line.
	{
		name:    "monitor/wait-bad-selector",
		setup:   nil,
		args:    []string{"monitor", "wait", "T-09999999", "--until", "state=completed"},
		mutates: false,
	},
	// monitor wait: invalid --until condition → validation error (exit 2).
	{
		name:    "monitor/wait-bad-condition",
		setup:   [][]string{{"touch", "inbox/open", "-t", "open"}},
		args:    []string{"monitor", "wait", "inbox/open", "--until", "bogus"},
		mutates: false,
	},
	// monitor watch --until: condition already met → exactly one terminal line,
	// exit 0. Race-tolerant: eventsView + stateView are two snapshots.
	{
		name:    "monitor/watch-until-met",
		setup:   [][]string{{"touch", "inbox/done", "-t", "done"}, {"set", "inbox/done", "--state", "completed"}},
		args:    []string{"monitor", "watch", "inbox/done", "--until", "state=completed", "--timeout", "5s"},
		mutates: false,
	},
	// monitor watch --until: not met within a short timeout → terminal result=timeout,
	// exit 1. Bounded so the streaming loop terminates.
	{
		name:    "monitor/watch-until-timeout",
		setup:   [][]string{{"touch", "inbox/open", "-t", "open"}},
		args:    []string{"monitor", "watch", "inbox/open", "--until", "state=completed", "--timeout", "150ms"},
		mutates: false,
	},
	// monitor watch --until: no selector → usage error (exit 2) before any streaming.
	{
		name:    "monitor/watch-until-no-selector",
		setup:   nil,
		args:    []string{"monitor", "watch", "--until", "state=completed"},
		mutates: false,
	},
	// monitor watch: invalid --format → usage error (exit 2).
	{
		name:    "monitor/watch-bad-format",
		setup:   [][]string{{"touch", "inbox/open", "-t", "open"}},
		args:    []string{"monitor", "watch", "inbox/open", "--format", "bogus"},
		mutates: false,
	},
	// monitor watch --scope is a legacy hard gate, not an RPC-only narrowing:
	// non-raw watch returns the single-print usage error before streaming. The raw
	// branch bypasses this gate and follows indefinitely, so it is documented rather
	// than placed in this bounded parity harness.
	{
		name:    "monitor/watch-scope-legacy-gate",
		setup:   [][]string{{"touch", "inbox/open", "-t", "open"}},
		args:    []string{"monitor", "watch", "inbox/open", "--scope", "agent:cody:project:wrkq"},
		mutates: false,
	},

	// ── watch (bounded raw tail via wrkq.history.tailView) ──────────────────────
	// watch --follow=false --ndjson: drain the current backlog as NDJSON and exit 0.
	// The deprecation warning is printed caller-side to stderr. normalizeUUID +
	// normalize() neutralize payload UUIDs + timestamps.
	{
		name:          "watch/ndjson-no-follow",
		setup:         [][]string{{"touch", "inbox/aa", "-t", "Alpha"}, {"touch", "inbox/bb", "-t", "Beta"}},
		args:          []string{"watch", "--ndjson", "--follow=false"},
		mutates:       false,
		normalizeUUID: true,
	},
	// watch --follow=false --since: start from a mid-log cursor, drain the remainder.
	{
		name:          "watch/since-no-follow",
		setup:         [][]string{{"touch", "inbox/aa", "-t", "Alpha"}, {"touch", "inbox/bb", "-t", "Beta"}},
		args:          []string{"watch", "--since", "1", "--ndjson", "--follow=false"},
		mutates:       false,
		normalizeUUID: true,
	},
	// watch --follow=false on an empty backlog (since past the end) → no events, exit 0
	// (still prints the deprecation warning to stderr).
	{
		name:    "watch/empty-no-follow",
		setup:   [][]string{{"touch", "inbox/aa", "-t", "Alpha"}},
		args:    []string{"watch", "--since", "9999", "--ndjson", "--follow=false"},
		mutates: false,
	},
	// ── search + index family (T-05114) ──────────────────────────────────────
	// SERVER-OWNED sidecar + embedder behind wrkq.search.* / wrkq.index.*. All
	// cases use the deterministic `none` dense provider (pure lexical/FTS — no
	// llama, no non-determinism), so old-vs-new byte-parity holds for status,
	// lexical search, stale, and lifecycle. Forced --pretty renders the human
	// layout without ANSI in the non-TTY harness and is byte-proven here.

	// index status: a fresh per-dir sidecar (empty/stale) — deterministic JSON.
	{
		name:        "index/status-empty-json",
		setup:       [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
		args:        []string{"index", "status", "--json"},
		searchIndex: true,
	},
	// index status: non-TTY default is JSON (no flag).
	{
		name:        "index/status-empty-default",
		setup:       [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
		args:        []string{"index", "status"},
		searchIndex: true,
	},
	// index rebuild: rebuilds from identical canonical data → identical ack JSON
	// (map-alphabetical rebuilt/status). Each binary rebuilds its own per-dir sidecar.
	{
		name:        "index/rebuild",
		setup:       [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task", "-d", "indexable body text"}},
		args:        []string{"index", "rebuild"},
		searchIndex: true,
	},
	// index update: no pending changes after the seed's implicit state → deterministic.
	{
		name:        "index/update",
		setup:       [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
		args:        []string{"index", "update"},
		searchIndex: true,
	},
	// index vacuum: deterministic ack (map-alphabetical vacuumed).
	{
		name:        "index/vacuum",
		setup:       [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
		args:        []string{"index", "vacuum"},
		searchIndex: true,
	},
	// index pause / resume: deterministic acks (map-alphabetical status).
	{
		name:        "index/pause",
		setup:       [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
		args:        []string{"index", "pause"},
		searchIndex: true,
	},
	{
		name:        "index/resume",
		setup:       [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}, {"index", "pause"}},
		args:        []string{"index", "resume"},
		searchIndex: true,
	},

	// search: empty index (no rebuild seeded) → stale, empty results. Both the
	// non-TTY default (NDJSON) and --json forms are deterministic with no rows.
	{
		name:        "search/empty-default",
		setup:       [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
		args:        []string{"search", "searchable"},
		searchIndex: true,
	},
	{
		name:        "search/empty-json",
		setup:       [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
		args:        []string{"search", "searchable", "--json"},
		searchIndex: true,
	},
	{
		name:        "search/output-table-unsupported",
		setup:       [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
		args:        []string{"search", "searchable", "--output", "table"},
		searchIndex: true,
	},
	{
		name:        "search/output-yaml-unsupported",
		setup:       [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
		args:        []string{"search", "searchable", "--output", "yaml"},
		searchIndex: true,
	},
	{
		name:        "search/output-tsv-unsupported",
		setup:       [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
		args:        []string{"search", "searchable", "--output", "tsv"},
		searchIndex: true,
	},

	// search WITH a prebuilt shared sidecar: the seed `index rebuild` populates a
	// shared absolute sidecar; the read-only `search` finds the lexical match on
	// both binaries. Covers json / ndjson / stale / path / assignee / sort.
	{
		name:              "search/json-hit",
		setup:             [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task", "-d", "alpha beta gamma"}, {"index", "rebuild"}},
		args:              []string{"search", "alpha", "--json"},
		searchIndex:       true,
		searchSeedRebuild: true,
	},
	{
		name:              "search/ndjson-hit",
		setup:             [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task", "-d", "alpha beta gamma"}, {"index", "rebuild"}},
		args:              []string{"search", "alpha", "--ndjson"},
		searchIndex:       true,
		searchSeedRebuild: true,
	},
	{
		name:              "search/pretty",
		setup:             [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task", "-d", "alpha beta gamma"}, {"index", "rebuild"}},
		args:              []string{"search", "alpha", "--pretty"},
		searchIndex:       true,
		searchSeedRebuild: true,
	},
	{
		name:              "search/fresh-after-rebuild",
		setup:             [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task", "-d", "alpha beta gamma"}, {"index", "rebuild"}},
		args:              []string{"search", "alpha", "--fresh", "--json"},
		searchIndex:       true,
		searchSeedRebuild: true,
	},
	{
		name:              "search/path-filter",
		setup:             [][]string{{"mkdir", "proj"}, {"touch", "proj/find-me", "-t", "Searchable Task", "-d", "alpha beta gamma"}, {"index", "rebuild"}},
		args:              []string{"search", "alpha", "proj", "--json"},
		searchIndex:       true,
		searchSeedRebuild: true,
	},
	{
		name:              "search/sort-updated",
		setup:             [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task", "-d", "alpha beta gamma"}, {"index", "rebuild"}},
		args:              []string{"search", "alpha", "--sort", "updated_at", "--json"},
		searchIndex:       true,
		searchSeedRebuild: true,
	},
	{
		name:              "search/state-all",
		setup:             [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task", "-d", "alpha beta gamma"}, {"index", "rebuild"}},
		args:              []string{"search", "alpha", "--state", "all", "--json"},
		searchIndex:       true,
		searchSeedRebuild: true,
	},
	// --assignee with a BARE agent slug: legacy normalizes `clod` → `agent:clod`
	// before filtering; the server must do the same compat normalization or the
	// assigned hit disappears (daedalus #10263, T-05114). The seed assigns the task
	// to agent:clod so the filter must match on both binaries.
	{
		name:              "search/assignee-bare",
		setup:             [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task", "-d", "alpha beta gamma", "--assignee", "clod"}, {"index", "rebuild"}},
		args:              []string{"search", "alpha", "--assignee", "clod", "--json"},
		searchIndex:       true,
		searchSeedRebuild: true,
	},
	// --assignee with a CANONICAL ref: server-side normalization must be idempotent
	// so this finds the same hit as the bare form.
	{
		name:              "search/assignee-canonical",
		setup:             [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task", "-d", "alpha beta gamma", "--assignee", "clod"}, {"index", "rebuild"}},
		args:              []string{"search", "alpha", "--assignee", "agent:clod", "--json"},
		searchIndex:       true,
		searchSeedRebuild: true,
	},
	// search disabled: WRKQ_SEARCH_ENABLED=0 → legacy "search is disabled" error
	// on both binaries (the mirror surfaces the server message raw).
	{
		name:  "search/disabled-errors",
		setup: [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
		args:  []string{"search", "alpha"},
		env:   []string{"WRKQ_SEARCH_ENABLED=0"},
	},
}

// attachSrcFiles are the host source files materialized into each run/seed dir
// for the attach put/ls cases. Distinct extensions exercise MIME auto-detection
// (.txt → text/plain, .md → text/markdown, .bin → application/octet-stream) and
// distinct byte lengths so size_bytes differs per row.
var attachSrcFiles = map[string]string{
	"alpha.txt": "alpha attachment body\n",
	"beta.md":   "# beta\n\nmarkdown attachment\n",
	"gamma.bin": "gamma\x00\x01\x02bytes",
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

// logSeed builds a fixture for the `log` history read model: a task (one
// task.created event), a top-level container P-00002 (one container.created
// event), and a relation (adds a task.relation.created event to the task).
// Friendly IDs are deterministic: inbox=P-00001 (seeded), proj=P-00002,
// the first task=T-00001.
var logSeed = [][]string{
	{"touch", "inbox/log-task", "-t", "Log Task", "--priority", "2"},
	{"mkdir", "proj"},
	{"touch", "inbox/log-blocker", "-t", "Blocker"},
	{"relation", "add", "inbox/log-task", "blocks", "inbox/log-blocker"},
}

// logSeedMultiEvent gives the container P-00002 MORE THAN ONE event_log row
// (create + a title update + a rename) so the --porcelain --limit 1 case exercises
// a real next_cursor over event_log.id DESC (page 1 of N, cursor emitted to stderr).
var logSeedMultiEvent = [][]string{
	{"mkdir", "proj"},
	{"container", "set", "proj", "--webhook-urls", `["https://a.test"]`},
	{"rename-container", "proj", "renamed-proj"},
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
		t.Skip("builds wrkq-legacy + wrkq-rpccli and runs both CLIs")
	}
	bins := buildParityBinaries(t)
	for _, tc := range parityCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Seed ONE base fixture, then give each binary a byte-identical copy
			// (incl. random UUIDs), so output comparison catches real id bugs
			// rather than masking legitimately-different independent seeds.
			seedEnv := append([]string{}, tc.seedEnv...)
			runEnv := tc.env
			if tc.seededAttachStore {
				// Shared absolute store: the seed `attach put` writes the bytes here;
				// both get-runs read from the SAME dir (the copied DB's relative_path is
				// dir-independent). Computed at run time so it cannot live in the static
				// case literal.
				store := filepath.Join(t.TempDir(), "attach-store")
				seedEnv = append(seedEnv, "WRKQ_ATTACH_DIR="+store)
				runEnv = append(append([]string{}, tc.env...), "WRKQ_ATTACH_DIR="+store)
			}
			if tc.searchIndex {
				// Deterministic hermetic search host: ON + `none` dense provider (pure
				// lexical/FTS, no llama, no non-determinism) on BOTH seed + run. A SHARED
				// ABSOLUTE sidecar (WRKQ_SEARCH_DB_PATH) is always used so the index
				// status `path` field is dir-independent and byte-identical across the
				// old/new run dirs (the default <db>.search.sqlite would embed each
				// binary's distinct copied-fixture dir). The shared sidecar is safe: the
				// old + new runs are sequential, and every exercised index op is
				// idempotent (rebuild/update/vacuum/pause/resume) or read-only
				// (status/search). When searchSeedRebuild is set the seed `index rebuild`
				// also populates this SAME sidecar so a read-only `search` finds rows.
				sidecar := filepath.Join(t.TempDir(), "search.sqlite")
				searchBase := []string{
					"WRKQ_SEARCH_ENABLED=1",
					"WRKQ_SEARCH_DENSE_PROVIDER=none",
					"WRKQ_SEARCH_DB_PATH=" + sidecar,
				}
				seedEnv = append(append([]string{}, seedEnv...), searchBase...)
				runEnv = append(append([]string{}, runEnv...), searchBase...)
			}
			base := seedFixtureFilesEnv(t, bins, tc.setup, tc.files, seedEnv)
			oldDir := copyFixture(t, base)
			newDir := copyFixture(t, base)
			writeRunFiles(t, oldDir, tc.files)
			writeRunFiles(t, newDir, tc.files)

			oldRes := runCLIStdin(t, bins.wrkq, oldDir, tc.args, runEnv, tc.stdin)
			newRes := runCLIStdin(t, bins.mirror, newDir, tc.args, runEnv, tc.stdin)

			if oldRes.exit != newRes.exit {
				t.Errorf("exit code: old=%d new=%d\n old stderr: %s\n new stderr: %s",
					oldRes.exit, newRes.exit, oldRes.stderr, newRes.stderr)
			}
			norm := func(s string) string {
				s = normalize(s)
				if tc.normalizeUUID {
					s = uuidRe.ReplaceAllString(s, "<UUID>")
				}
				if tc.normalizeRunDir {
					s = strings.ReplaceAll(s, oldDir, "<RUN_DIR>")
					s = strings.ReplaceAll(s, newDir, "<RUN_DIR>")
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

func TestAgentContextNoDBParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds wrkq-legacy + wrkq-rpccli and runs both CLIs")
	}
	bins := buildParityBinaries(t)
	dbPath := filepath.Join(t.TempDir(), "missing.db")
	run := func(bin string) cliResult {
		cmd := exec.Command(bin, "--db", dbPath, "--as", "agent:local-human", "agent-context", "--json")
		cmd.Env = append(hermeticEnv(), "ASP_SCOPE_REF=agent:cody:project:wrkq")
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
	oldRes := run(bins.wrkq)
	newRes := run(bins.mirror)
	if oldRes.exit != newRes.exit {
		t.Fatalf("exit code: old=%d new=%d\nold stderr=%q\nnew stderr=%q", oldRes.exit, newRes.exit, oldRes.stderr, newRes.stderr)
	}
	if oldRes.stdout != newRes.stdout {
		t.Fatalf("stdout mismatch:\nold=%q\nnew=%q", oldRes.stdout, newRes.stdout)
	}
	if oldRes.stderr != newRes.stderr {
		t.Fatalf("stderr mismatch:\nold=%q\nnew=%q", oldRes.stderr, newRes.stderr)
	}
}

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

// TestCommentLsMultiTaskCursorReplay proves the multi-task `comment ls` paging
// contract. Legacy applies the same cursor predicate + limit+1 to EACH task's
// query, accumulates rows in task order, then truncates the combined set at limit
// and builds the next cursor from the last surviving row — a non-obvious paging
// shape. This replays the cursor across pages over TWO tasks and asserts per-page
// byte parity old-vs-new plus no-dup/no-miss (concatenated pages == unpaginated).
func TestCommentLsMultiTaskCursorReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs")
	}
	bins := buildParityBinaries(t)
	setup := [][]string{{"touch", "inbox/pa", "-t", "PA"}, {"touch", "inbox/pb", "-t", "PB"}}
	for _, b := range []string{"a1", "a2", "a3"} {
		setup = append(setup, []string{"comment", "add", "inbox/pa", b})
	}
	for _, b := range []string{"b1", "b2", "b3"} {
		setup = append(setup, []string{"comment", "add", "inbox/pb", b})
	}
	base := seedFixture(t, bins, setup)
	oldDir := copyFixture(t, base)
	newDir := copyFixture(t, base)

	tasks := []string{"inbox/pa", "inbox/pb"}
	paginateAll := func(bin, dir string) (all, pages []string) {
		cur := ""
		for {
			args := append([]string{"comment", "ls"}, tasks...)
			args = append(args, "--porcelain", "--limit", "2")
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

	fullArgs := append([]string{"comment", "ls"}, tasks...)
	oldFull := nonEmptyLines(runCLI(t, bins.wrkq, oldDir, fullArgs).stdout)
	newFull := nonEmptyLines(runCLI(t, bins.mirror, newDir, fullArgs).stdout)
	if strings.Join(oldAll, "\n") != strings.Join(oldFull, "\n") {
		t.Errorf("OLD multi-task paginated != unpaginated (dup/miss):\n paged: %v\n full: %v", oldAll, oldFull)
	}
	if strings.Join(newAll, "\n") != strings.Join(newFull, "\n") {
		t.Errorf("NEW multi-task paginated != unpaginated (dup/miss):\n paged: %v\n full: %v", newAll, newFull)
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

// TestLsMultiPathCursorReplay proves the ls MULTI-PATH cursor contract: the SERVER
// owns combined paging over the merge-sorted union of multiple path args (the client
// must NOT sort/page per-argument). It asserts per-page byte parity old-vs-new and
// no-dup/no-miss (concatenated pages equal the unpaginated combined output) over a
// genuine two-container-path fixture. daedalus requires this distinct from the
// single-path TestLsCursorReplay (multi-path first-page parity does not prove replay).
func TestLsMultiPathCursorReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs")
	}
	bins := buildParityBinaries(t)
	// Two top-level projects, each with a child container + two tasks. Combined
	// merge-sort by slug over BOTH paths: ca, cb, ta-1, ta-2, tb-1, tb-2 → pages 2,2,2.
	setup := [][]string{
		{"mkdir", "pa"}, {"mkdir", "pb"},
		{"mkdir", "pa/ca"}, {"mkdir", "pb/cb"},
		{"touch", "pa/ta-1", "-t", "ta-1"}, {"touch", "pa/ta-2", "-t", "ta-2"},
		{"touch", "pb/tb-1", "-t", "tb-1"}, {"touch", "pb/tb-2", "-t", "tb-2"},
	}
	base := seedFixture(t, bins, setup)
	oldDir := copyFixture(t, base)
	newDir := copyFixture(t, base)

	paths := []string{"pa", "pb"}
	paginateAll := func(bin, dir string) (all, pages []string) {
		cur := ""
		for {
			args := append([]string{"ls"}, paths...)
			args = append(args, "--porcelain", "--limit", "2")
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

	fullArgs := append([]string{"ls"}, paths...)
	oldFull := nonEmptyLines(runCLI(t, bins.wrkq, oldDir, fullArgs).stdout)
	newFull := nonEmptyLines(runCLI(t, bins.mirror, newDir, fullArgs).stdout)
	if strings.Join(oldAll, "\n") != strings.Join(oldFull, "\n") {
		t.Errorf("OLD multi-path paginated != unpaginated (dup/miss):\n paged: %v\n full: %v", oldAll, oldFull)
	}
	if strings.Join(newAll, "\n") != strings.Join(newFull, "\n") {
		t.Errorf("NEW multi-path paginated != unpaginated (dup/miss):\n paged: %v\n full: %v", newAll, newFull)
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

// TestLogCursorReplay proves the `log` history read-model cursor contract over the
// event_log id-DESC ordering: cursor replay across page boundaries, per-page byte
// parity old-vs-new, and no-dup/no-miss (concatenated pages equal the unpaginated
// history). daedalus REQUIRES this distinct from the first-page parity case. The
// container target accumulates several event_log rows (create + title update +
// rename) so paging over id DESC is genuinely exercised. NDJSON is the comparison
// substrate (deterministic; the oneline/detailed/--patch Summary lines iterate a
// payload map and are non-byte-stable in legacy itself).
func TestLogCursorReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs")
	}
	bins := buildParityBinaries(t)
	// proj = P-00002 (inbox is P-00001). Four container events: created, updated
	// (webhook a), updated (webhook b), updated (rename) → ids DESC paginate 2,2.
	setup := [][]string{
		{"mkdir", "proj"},
		{"container", "set", "proj", "--webhook-urls", `["https://a.test"]`},
		{"container", "set", "proj", "--webhook-urls", `["https://b.test"]`},
		{"rename-container", "proj", "renamed"},
	}
	base := seedFixture(t, bins, setup)
	oldDir := copyFixture(t, base)
	newDir := copyFixture(t, base)

	paginateAll := func(bin, dir string) (all, pages []string) {
		cur := ""
		for {
			args := []string{"log", "P-00002", "--porcelain", "--limit", "2"}
			if cur != "" {
				args = append(args, "--cursor", cur)
			}
			res := runCLI(t, bin, dir, args)
			if res.exit != 0 {
				t.Fatalf("%s page (cursor=%q) exit %d: %s", bin, cur, res.exit, res.stderr)
			}
			all = append(all, nonEmptyLines(res.stdout)...)
			pages = append(pages, normalize(res.stdout)+"\x1e"+res.stderr)
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

	oldFull := nonEmptyLines(normalize(runCLI(t, bins.wrkq, oldDir, []string{"log", "P-00002"}).stdout))
	newFull := nonEmptyLines(normalize(runCLI(t, bins.mirror, newDir, []string{"log", "P-00002"}).stdout))
	oldAllN := nonEmptyLines(normalize(strings.Join(oldAll, "\n")))
	newAllN := nonEmptyLines(normalize(strings.Join(newAll, "\n")))
	if strings.Join(oldAllN, "\n") != strings.Join(oldFull, "\n") {
		t.Errorf("OLD paginated != unpaginated (dup/miss):\n paged: %v\n full: %v", oldAllN, oldFull)
	}
	if strings.Join(newAllN, "\n") != strings.Join(newFull, "\n") {
		t.Errorf("NEW paginated != unpaginated (dup/miss):\n paged: %v\n full: %v", newAllN, newFull)
	}
	if len(oldAll) != 4 || len(newAll) != 4 {
		t.Errorf("expected 4 rows across pages: old=%d new=%d", len(oldAll), len(newAll))
	}
}

// TestLogHumanModesSemanticParity is the order-insensitive SEMANTIC guard daedalus
// requires (hrcchat#10142) for log's exposed-but-non-byte-stable human modes
// (--oneline, --patch). Those modes render the decoded payload MAP, whose key order
// Go randomizes, so legacy's OWN output is not byte-stable for multi-key payloads — a
// byte-equality test would be flaky. Instead, over a MULTI-KEY-payload fixture, we
// assert both binaries SUCCEED and carry the SAME content (identical token multiset:
// event ids/types/actors/payload key=value pairs), comparing order-insensitively
// rather than byte-for-byte. NOT a byte-equality test (deliberately).
func TestLogHumanModesSemanticParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs")
	}
	bins := buildParityBinaries(t)
	// T-00001 (under inbox=P-00001): a create event + a MULTI-KEY update event
	// (state+priority in one set). Payloads are short, so --oneline does not
	// truncate (truncation would drop different pairs per random map order, which
	// is genuine legacy nondeterminism, not a mirror divergence).
	setup := [][]string{
		{"touch", "inbox/log-multi", "-t", "Multi", "--priority", "3"},
		{"set", "inbox/log-multi", "--state", "in_progress", "--priority", "1"},
	}
	base := seedFixture(t, bins, setup)
	oldDir := copyFixture(t, base)
	newDir := copyFixture(t, base)

	sameMultiset := func(a, b map[string]int) bool {
		if len(a) != len(b) {
			return false
		}
		for k, c := range a {
			if b[k] != c {
				return false
			}
		}
		return true
	}

	// Legacy renders these modes by iterating the decoded payload map (random order),
	// and `formatEventSummary` truncates any summary >60 chars to `[:57]+"..."` — so the
	// one-line "Summary:" string is BOTH order-dependent AND lossy. We therefore compare
	// only what is deterministic+lossless per mode, order-insensitively (never byte order).
	//
	// --patch detailed output is, per event: fixed-order header lines (Event/Timestamp/
	// Principal/Scope/Actor/ETag), one truncated "  Summary:" line, then a "  Changes:"
	// block of full "    key: value" payload lines (untruncated; only line ORDER is
	// random). patchLineSet keeps every non-empty line EXCEPT the truncated Summary, so
	// the multiset covers event headers (ids/types/actors) + the full payload pairs.
	patchLineSet := func(s string) map[string]int {
		m := map[string]int{}
		for _, ln := range nonEmptyLines(normalize(s)) {
			if strings.Contains(ln, "Summary:") {
				continue
			}
			m[strings.TrimSpace(ln)]++
		}
		return m
	}
	// --oneline is ONE truncated summary line per event; only its leading skeleton
	// (timestamp, event type, "by", actor) is deterministic. skeleton drops payload
	// key=value tokens and any truncated fragment.
	skeleton := func(s string) map[string]int {
		m := map[string]int{}
		for _, tok := range strings.Fields(normalize(s)) {
			if strings.Contains(tok, "=") || strings.Contains(tok, "...") {
				continue
			}
			m[strings.TrimRight(tok, ",")]++
		}
		return m
	}

	for _, mode := range []string{"--oneline", "--patch"} {
		args := []string{"log", "T-00001", mode}
		oldRes := runCLI(t, bins.wrkq, oldDir, args)
		newRes := runCLI(t, bins.mirror, newDir, args)
		if oldRes.exit != 0 {
			t.Fatalf("legacy log %s exit %d: %s", mode, oldRes.exit, oldRes.stderr)
		}
		if newRes.exit != 0 {
			t.Fatalf("mirror log %s exit %d: %s", mode, newRes.exit, newRes.stderr)
		}
		if strings.TrimSpace(oldRes.stdout) == "" {
			t.Fatalf("legacy log %s produced no output (multi-key fixture should render events)", mode)
		}
		if lo, ln := len(nonEmptyLines(oldRes.stdout)), len(nonEmptyLines(newRes.stdout)); lo != ln {
			t.Errorf("log %s line count differs: old=%d new=%d", mode, lo, ln)
		}
		switch mode {
		case "--patch":
			// Strong guard: event headers + full payload key=value pairs must match as a
			// set (excludes only the truncated Summary line).
			if om, nm := patchLineSet(oldRes.stdout), patchLineSet(newRes.stdout); !sameMultiset(om, nm) {
				t.Errorf("log --patch line multiset differs (excl. truncated Summary):\n old=%v\n new=%v", om, nm)
			}
		case "--oneline":
			// Summary is truncated → only the deterministic skeleton is comparable.
			if om, nm := skeleton(oldRes.stdout), skeleton(newRes.stdout); !sameMultiset(om, nm) {
				t.Errorf("log --oneline deterministic skeleton differs (event types/actors/timestamps):\n old=%v\n new=%v", om, nm)
			}
		}
	}
}

// TestLogDetailedTTYSemanticParity covers the exposed interactive default for
// `wrkq log <id>`. Like --patch, it renders Summary via decoded payload map
// iteration, so this is intentionally semantic rather than byte equality.
func TestLogDetailedTTYSemanticParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs on a pty")
	}
	bins := buildParityBinaries(t)
	setup := [][]string{
		{"touch", "inbox/log-multi", "-t", "Multi", "--priority", "3"},
		{"set", "inbox/log-multi", "--state", "in_progress", "--priority", "1"},
	}
	base := seedFixture(t, bins, setup)
	oldDir := copyFixture(t, base)
	newDir := copyFixture(t, base)

	oldOut, oldExit := runCLIOnTTY(t, bins.wrkq, oldDir, []string{"log", "T-00001"})
	newOut, newExit := runCLIOnTTY(t, bins.mirror, newDir, []string{"log", "T-00001"})
	if oldExit != 0 || newExit != 0 {
		t.Fatalf("log TTY exits: old=%d out=%q; new=%d out=%q", oldExit, oldOut, newExit, newOut)
	}
	if strings.TrimSpace(oldOut) == "" || strings.TrimSpace(newOut) == "" {
		t.Fatalf("log TTY produced empty output: old=%q new=%q", oldOut, newOut)
	}

	lineSet := func(s string) map[string]int {
		m := map[string]int{}
		for _, ln := range nonEmptyLines(normalize(s)) {
			if strings.Contains(ln, "Summary:") {
				continue
			}
			m[strings.TrimSpace(ln)]++
		}
		return m
	}
	oldSet := lineSet(oldOut)
	newSet := lineSet(newOut)
	if len(oldSet) != len(newSet) {
		t.Fatalf("log TTY skeleton line-set size differs:\nold=%v\nnew=%v", oldSet, newSet)
	}
	for line, count := range oldSet {
		if newSet[line] != count {
			t.Fatalf("log TTY skeleton differs on %q: old=%d new=%d\nold=%v\nnew=%v", line, count, newSet[line], oldSet, newSet)
		}
	}
	for _, want := range []string{"\x1b[33mEvent ", "task.created", "task.updated", "Principal:", "ETag:"} {
		if !strings.Contains(oldOut, want) {
			t.Fatalf("legacy log TTY output missing %q (got %q)", want, oldOut)
		}
	}
}

// TestFindHardGates RETIRED (E2): find's --print0 + table/human/yaml/tsv render
// modes are now byte-proven against legacy in TestParity (find/print0,
// find/output-table|human|yaml|tsv), and --output raw + conflicting-modes are
// byte-parity error cases. No find surface remains hard-gated.

// TestCpRecursiveHardGate is retained as a named guard for the broad rpccli
// suite. Live legacy code accepts -r/--recursive but still resolves only source
// tasks, so the former mirror-only hard gate was removed and the behavior now
// lives in TestParity/cp/recursive-* rows.
func TestCpRecursiveHardGate(t *testing.T) {
}

// TestRmContainerHardGate is retained as a named guard for the broad rpccli suite.
// The old gate is retired: container rm now has real byte parity in TestParity
// (rm/container-*), using wrkq.container.archive for default soft-delete and
// wrkq.container.delete for empty-container purge.
func TestRmContainerHardGate(t *testing.T) {
}

// TestRestoreContainerHardGate is retained as a named guard for the broad rpccli
// suite. The old gate is retired: container restore now has real byte parity in
// TestParity (restore/container-*), using wrkq.container.restore.
func TestRestoreContainerHardGate(t *testing.T) {
}

// TestLsHardGates previously proved the mirror REFUSED the legacy ls surfaces it
// did not yet implement. All of those (table/human/yaml/tsv, --one/--nul,
// --recursive, multi-path, conflicting-modes) now have REAL byte parity and live
// in TestParity (ls/output-*, ls/one, ls/nul, ls/recursive-noop, ls/multi-path*,
// ls/conflicting-modes-errors). No ls surface remains hard-gated, so the gate test
// is retired — its coverage moved into the equivalence harness.

// TestTreeHardGates is retained as a named guard for the broad rpccli suite. The
// previous gates moved into TestParity: output modes, extra paths, --fields, and
// conflicting modes now match legacy behavior. No tree surface remains hard-gated.
func TestTreeHardGates(t *testing.T) {
}

// TestContainerSetTTYHumanParity proves the TTY-only human renderers for
// per-container and --all webhook URL updates. The pipe-based parity harness
// covers non-TTY indented JSON and validation errors; this attaches stdout/stderr
// to a real pty so legacy and mirror both take the interactive branch.
func TestContainerSetTTYHumanParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs the mirror CLI")
	}
	bins := buildParityBinaries(t)
	tests := []struct {
		name      string
		setup     [][]string
		args      []string
		verifyArg []string
	}{
		{
			name:      "single",
			setup:     [][]string{{"mkdir", "hooked"}},
			args:      []string{"container", "set", "hooked", "--webhook-url", "https://example.test/a"},
			verifyArg: []string{"container", "cat", "hooked", "--json"},
		},
		{
			name:      "all",
			setup:     [][]string{{"mkdir", "alpha"}, {"mkdir", "beta"}},
			args:      []string{"container", "set", "--all", "--add-webhook-url", "https://example.test/all"},
			verifyArg: []string{"container", "cat", "alpha", "--json"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := seedFixture(t, bins, tc.setup)
			oldDir := copyFixture(t, base)
			newDir := copyFixture(t, base)

			oldOut, oldExit := runCLIOnTTY(t, bins.wrkq, oldDir, tc.args)
			newOut, newExit := runCLIOnTTY(t, bins.mirror, newDir, tc.args)
			if oldExit != newExit || oldOut != newOut {
				t.Fatalf("TTY mismatch\nold exit=%d out=%q\nnew exit=%d out=%q", oldExit, oldOut, newExit, newOut)
			}
			oldVerify := runCLI(t, bins.wrkq, oldDir, tc.verifyArg)
			newVerify := runCLI(t, bins.mirror, newDir, tc.verifyArg)
			if oldVerify.exit != newVerify.exit || oldVerify.stdout != newVerify.stdout || oldVerify.stderr != newVerify.stderr {
				t.Fatalf("post-mutation container cat mismatch\nold=%+v\nnew=%+v", oldVerify, newVerify)
			}
		})
	}
}

// TestRelationLsTTYTableParity proves the legacy-only TTY render path. `relation
// ls` emits the padded human TABLE (Direction/Kind/Task ID/Slug/Title) ONLY when
// stdout is an interactive terminal — unreachable through the bytes.Buffer harness,
// which always reports non-TTY. So each binary runs with stdout attached to a real
// pseudo-terminal and the captured terminal bytes (incl. the pty's identical
// \n→\r\n translation) are byte-compared old-vs-new. Covers the populated table
// (outgoing+incoming, multi-kind ordering) and the empty "No relations found" line.
func TestRelationLsTTYTableParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs on a pty")
	}
	bins := buildParityBinaries(t)

	cases := []struct {
		name  string
		setup [][]string
		args  []string
	}{
		{
			// Outgoing (blocks, relates_to) + incoming, exercising the multi-kind,
			// multi-direction ordering and column padding of the table.
			name: "populated",
			setup: [][]string{
				{"touch", "inbox/ra", "-t", "Alpha task"},
				{"touch", "inbox/rb", "-t", "Beta"},
				{"touch", "inbox/rc", "-t", "Gamma the third"},
				{"relation", "add", "inbox/ra", "blocks", "inbox/rb"},
				{"relation", "add", "inbox/ra", "relates_to", "inbox/rc"},
				{"relation", "add", "inbox/rc", "blocks", "inbox/ra"},
			},
			args: []string{"relation", "ls", "inbox/ra"},
		},
		{
			name:  "empty",
			setup: [][]string{{"touch", "inbox/ra", "-t", "Alpha"}},
			args:  []string{"relation", "ls", "inbox/ra"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := seedFixture(t, bins, tc.setup)
			oldDir := copyFixture(t, base)
			newDir := copyFixture(t, base)

			oldOut, oldExit := runCLIOnTTY(t, bins.wrkq, oldDir, tc.args)
			newOut, newExit := runCLIOnTTY(t, bins.mirror, newDir, tc.args)

			if oldExit != newExit {
				t.Errorf("exit code: old=%d new=%d\n old: %q\n new: %q", oldExit, newExit, oldOut, newOut)
			}
			if normalize(newOut) != normalize(oldOut) {
				t.Errorf("tty table mismatch:\n old: %q\n new: %q", oldOut, newOut)
			}
			// Guard against a false "both produced nothing/identical-error" parity:
			// assert the legacy oracle actually rendered the expected TTY surface.
			if tc.name == "populated" {
				for _, want := range []string{"Direction", "Kind", "Task ID", "Slug", "Title", "blocks", "relates_to"} {
					if !strings.Contains(oldOut, want) {
						t.Fatalf("legacy TTY table missing %q (got %q)", want, oldOut)
					}
				}
			} else if !strings.Contains(oldOut, "No relations found") {
				t.Fatalf("legacy empty TTY output missing sentinel (got %q)", oldOut)
			}
		})
	}
}

// TestDiffTTYHumanParity proves the interactive human/color renderer for
// `wrkq diff`. Non-TTY diff defaults to JSON in TestParity; a real TTY takes the
// colorized human branch.
func TestDiffTTYHumanParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs on a pty")
	}
	bins := buildParityBinaries(t)

	cases := []struct {
		name  string
		setup [][]string
		args  []string
		want  []string
	}{
		{
			name: "changed",
			setup: [][]string{
				{"touch", "inbox/da", "-t", "Title A", "--priority", "2", "-d", "desc a"},
				{"touch", "inbox/db", "-t", "Title B", "--priority", "1", "-d", "desc b"},
			},
			args: []string{"diff", "inbox/da", "inbox/db"},
			want: []string{"Comparing T-00001 (da) vs T-00002 (db)", "4 field(s) changed:",
				"\x1b[33mslug:\x1b[0m", "\x1b[31m- Title A\x1b[0m", "\x1b[32m+ Title B\x1b[0m"},
		},
		{
			name:  "same",
			setup: [][]string{{"touch", "inbox/da", "-t", "Same"}},
			args:  []string{"diff", "inbox/da", "inbox/da"},
			want:  []string{"No differences between T-00001 and T-00001"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := seedFixture(t, bins, tc.setup)
			oldDir := copyFixture(t, base)
			newDir := copyFixture(t, base)

			oldOut, oldExit := runCLIOnTTY(t, bins.wrkq, oldDir, tc.args)
			newOut, newExit := runCLIOnTTY(t, bins.mirror, newDir, tc.args)

			if oldExit != newExit {
				t.Errorf("exit code: old=%d new=%d\n old: %q\n new: %q", oldExit, newExit, oldOut, newOut)
			}
			if normalize(newOut) != normalize(oldOut) {
				t.Errorf("tty diff human mismatch:\n old: %q\n new: %q", oldOut, newOut)
			}
			for _, want := range tc.want {
				if !strings.Contains(oldOut, want) {
					t.Fatalf("legacy diff TTY output missing %q (got %q)", want, oldOut)
				}
			}
		})
	}
}

// TestAttachLsTTYTableParity proves the legacy-only TTY table for `attach ls`.
// Non-TTY attach ls emits NDJSON; an interactive terminal renders a padded table.
func TestAttachLsTTYTableParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs on a pty")
	}
	bins := buildParityBinaries(t)

	cases := []struct {
		name  string
		setup [][]string
		files map[string]string
		args  []string
	}{
		{
			name:  "empty",
			setup: [][]string{{"touch", "inbox/at", "-t", "Attach Task"}},
			args:  []string{"attach", "ls", "inbox/at"},
		},
		{
			name: "populated",
			setup: [][]string{
				{"touch", "inbox/at", "-t", "Attach Task"},
				{"attach", "put", "inbox/at", "alpha.txt"},
				{"attach", "put", "inbox/at", "beta.md"},
			},
			files: attachSrcFiles,
			args:  []string{"attach", "ls", "inbox/at"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := seedFixtureFiles(t, bins, tc.setup, tc.files)
			oldDir := copyFixture(t, base)
			newDir := copyFixture(t, base)

			oldOut, oldExit := runCLIOnTTY(t, bins.wrkq, oldDir, tc.args)
			newOut, newExit := runCLIOnTTY(t, bins.mirror, newDir, tc.args)

			if oldExit != newExit {
				t.Errorf("exit code: old=%d new=%d\n old: %q\n new: %q", oldExit, newExit, oldOut, newOut)
			}
			if normalize(newOut) != normalize(oldOut) {
				t.Errorf("tty attach table mismatch:\n old: %q\n new: %q", oldOut, newOut)
			}
			if tc.name == "populated" {
				for _, want := range []string{"ID", "Filename", "Size", "MIME Type", "Created", "alpha.txt", "beta.md"} {
					if !strings.Contains(oldOut, want) {
						t.Fatalf("legacy attach TTY table missing %q (got %q)", want, oldOut)
					}
				}
			} else if strings.TrimSpace(oldOut) != "" {
				t.Fatalf("legacy empty attach TTY output should be empty (got %q)", oldOut)
			}
		})
	}
}

// TestCheckBlockedTTYHumanParity proves the legacy TTY-only human output for
// `check blocked`: success text on stdout for an unblocked task, and the
// multi-line blocker report plus Cobra error line on stderr for a blocked task.
func TestCheckBlockedTTYHumanParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs on a pty")
	}
	bins := buildParityBinaries(t)

	cases := []struct {
		name  string
		setup [][]string
		args  []string
		want  []string
	}{
		{
			name:  "unblocked",
			setup: [][]string{{"touch", "inbox/free", "-t", "Free"}},
			args:  []string{"check", "blocked", "inbox/free"},
			want:  []string{"Task T-00001 is not blocked"},
		},
		{
			name: "blocked",
			setup: [][]string{
				{"touch", "inbox/main", "-t", "Main"},
				{"touch", "inbox/blk", "-t", "Blocker"},
				{"relation", "add", "inbox/blk", "blocks", "inbox/main"},
			},
			args: []string{"check", "blocked", "inbox/main"},
			want: []string{
				"Error: Task T-00001 is blocked by 1 incomplete task(s):",
				"  - T-00002: Blocker (state: open)",
				"Error: task T-00001 is blocked",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := seedFixture(t, bins, tc.setup)
			oldDir := copyFixture(t, base)
			newDir := copyFixture(t, base)

			oldOut, oldExit := runCLIOnTTY(t, bins.wrkq, oldDir, tc.args)
			newOut, newExit := runCLIOnTTY(t, bins.mirror, newDir, tc.args)

			if oldExit != newExit {
				t.Errorf("exit code: old=%d new=%d\n old: %q\n new: %q", oldExit, newExit, oldOut, newOut)
			}
			if normalize(newOut) != normalize(oldOut) {
				t.Errorf("tty check blocked mismatch:\n old: %q\n new: %q", oldOut, newOut)
			}
			for _, want := range tc.want {
				if !strings.Contains(oldOut, want) {
					t.Fatalf("legacy check blocked TTY output missing %q (got %q)", want, oldOut)
				}
			}
		})
	}
}

// TestCheckInboxTTYTableParity proves the legacy TTY-only table for
// `check-inbox`. Non-TTY check-inbox emits NDJSON, while an interactive terminal
// renders the padded ID/Slug/Title/State/Priority/Kind table.
func TestCheckInboxTTYTableParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs on a pty")
	}
	bins := buildParityBinaries(t)

	cases := []struct {
		name  string
		setup [][]string
		args  []string
	}{
		{
			name:  "empty",
			setup: nil,
			args:  []string{"check-inbox"},
		},
		{
			name: "populated",
			setup: [][]string{
				{"touch", "inbox/one", "-t", "One", "--priority", "2"},
				{"touch", "inbox/two", "-t", "Two", "--priority", "1"},
			},
			args: []string{"check-inbox"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := seedFixture(t, bins, tc.setup)
			oldDir := copyFixture(t, base)
			newDir := copyFixture(t, base)

			oldOut, oldExit := runCLIOnTTY(t, bins.wrkq, oldDir, tc.args)
			newOut, newExit := runCLIOnTTY(t, bins.mirror, newDir, tc.args)

			if oldExit != newExit {
				t.Errorf("exit code: old=%d new=%d\n old: %q\n new: %q", oldExit, newExit, oldOut, newOut)
			}
			if normalize(newOut) != normalize(oldOut) {
				t.Errorf("tty check-inbox table mismatch:\n old: %q\n new: %q", oldOut, newOut)
			}
			if tc.name == "populated" {
				for _, want := range []string{"ID", "Slug", "Title", "State", "Priority", "Kind", "two", "One"} {
					if !strings.Contains(oldOut, want) {
						t.Fatalf("legacy check-inbox TTY table missing %q (got %q)", want, oldOut)
					}
				}
			} else if strings.TrimSpace(oldOut) != "" {
				t.Fatalf("legacy empty check-inbox TTY output should be empty (got %q)", oldOut)
			}
		})
	}
}

// TestIndexTTYHumanParity proves the legacy TTY-only human renderers for the
// index command family. Non-TTY index commands emit JSON; interactive terminals
// render key:value status text or lifecycle one-liners.
func TestIndexTTYHumanParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs on a pty")
	}
	bins := buildParityBinaries(t)

	cases := []struct {
		name    string
		setup   [][]string
		args    []string
		want    []string
		seedEnv []string
	}{
		{
			name:  "status",
			setup: [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
			args:  []string{"index", "status"},
			want:  []string{"path: ", "status: ", "last_indexed_event_id:", "canonical_max_event_id:", "stale_event_count:", "chunks:"},
		},
		{
			name:  "rebuild",
			setup: [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task", "-d", "indexable body text"}},
			args:  []string{"index", "rebuild"},
			want:  []string{"rebuilt search index: ", " chunks, last event "},
		},
		{
			name:  "update",
			setup: [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
			args:  []string{"index", "update"},
			want:  []string{"updated search index: ", " chunks, last event "},
		},
		{
			name:  "vacuum",
			setup: [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
			args:  []string{"index", "vacuum"},
			want:  []string{"vacuumed search index"},
		},
		{
			name:  "pause",
			setup: [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}},
			args:  []string{"index", "pause"},
			want:  []string{"paused search indexing"},
		},
		{
			name:  "resume",
			setup: [][]string{{"touch", "inbox/find-me", "-t", "Searchable Task"}, {"index", "pause"}},
			args:  []string{"index", "resume"},
			want:  []string{"resumed search indexing"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sidecar := filepath.Join(t.TempDir(), "search.sqlite")
			searchEnv := []string{
				"WRKQ_SEARCH_ENABLED=1",
				"WRKQ_SEARCH_DENSE_PROVIDER=none",
				"WRKQ_SEARCH_DB_PATH=" + sidecar,
			}
			base := seedFixtureFilesEnv(t, bins, tc.setup, nil, append(searchEnv, tc.seedEnv...))
			oldDir := copyFixture(t, base)
			newDir := copyFixture(t, base)

			oldOut, oldExit := runCLIOnTTYEnv(t, bins.wrkq, oldDir, tc.args, searchEnv)
			newOut, newExit := runCLIOnTTYEnv(t, bins.mirror, newDir, tc.args, searchEnv)

			if oldExit != newExit {
				t.Errorf("exit code: old=%d new=%d\n old: %q\n new: %q", oldExit, newExit, oldOut, newOut)
			}
			if normalize(newOut) != normalize(oldOut) {
				t.Errorf("tty index human mismatch:\n old: %q\n new: %q", oldOut, newOut)
			}
			for _, want := range tc.want {
				if !strings.Contains(oldOut, want) {
					t.Fatalf("legacy index TTY output missing %q (got %q)", want, oldOut)
				}
			}
		})
	}
}

func TestTouchCreatesArtifactDirParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs CLIs")
	}
	bins := buildParityBinaries(t)
	base := seedFixture(t, bins, nil)
	oldDir := copyFixture(t, base)
	newDir := copyFixture(t, base)
	oldHome := filepath.Join(oldDir, "praesidium-home")
	newHome := filepath.Join(newDir, "praesidium-home")

	old := runCLIEnv(t, bins.wrkq, oldDir, []string{"touch", "inbox/artifact-old", "-t", "Artifact"}, []string{"PRAESIDIUM_HOME=" + oldHome})
	new := runCLIEnv(t, bins.mirror, newDir, []string{"touch", "inbox/artifact-new", "-t", "Artifact"}, []string{"PRAESIDIUM_HOME=" + newHome})
	if old.exit != 0 || new.exit != 0 {
		t.Fatalf("touch failed: old exit=%d stderr=%q; new exit=%d stderr=%q", old.exit, old.stderr, new.exit, new.stderr)
	}
	for name, home := range map[string]string{"old": oldHome, "new": newHome} {
		dir := filepath.Join(home, "var", "wrkq-artifacts", "T-00001")
		st, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("%s artifact dir missing at %s: %v", name, dir, err)
		}
		if !st.IsDir() {
			t.Fatalf("%s artifact path is not a directory: %s", name, dir)
		}
	}
}

func TestWatchFollowNDJSONParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + follows subprocesses")
	}
	bins := buildParityBinaries(t)
	base := seedFixture(t, bins, nil)
	oldDir := copyFixture(t, base)
	newDir := copyFixture(t, base)

	oldOut, oldErr := runFollowWithMutation(t, bins.wrkq, oldDir,
		[]string{"watch", "--ndjson", "--since", "0"},
		[]string{"touch", "inbox/follow", "-t", "Follow Task"},
	)
	newOut, newErr := runFollowWithMutation(t, bins.mirror, newDir,
		[]string{"watch", "--ndjson", "--since", "0"},
		[]string{"touch", "inbox/follow", "-t", "Follow Task"},
	)

	if !strings.Contains(oldErr, "wrkq watch is deprecated; use wrkq monitor watch --raw") {
		t.Fatalf("legacy watch follow stderr missing deprecation warning: %q", oldErr)
	}
	if normalize(newErr) != normalize(oldErr) {
		t.Fatalf("watch follow stderr mismatch:\n old: %q\n new: %q", oldErr, newErr)
	}
	if got, want := normalizeUUIDString(normalizeWatchFollowLines(newOut)), normalizeUUIDString(normalizeWatchFollowLines(oldOut)); got != want {
		t.Fatalf("watch follow stdout mismatch:\n old:\n%s\n new:\n%s", want, got)
	}
}

func TestMonitorWatchFollowNDJSONParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + follows subprocesses")
	}
	bins := buildParityBinaries(t)
	base := seedFixture(t, bins, nil)
	oldDir := copyFixture(t, base)
	newDir := copyFixture(t, base)

	oldOut, oldErr := runFollowWithMutation(t, bins.wrkq, oldDir,
		[]string{"monitor", "watch", "--since", "0", "--event-type", "task.created"},
		[]string{"touch", "inbox/follow", "-t", "Follow Task"},
	)
	newOut, newErr := runFollowWithMutation(t, bins.mirror, newDir,
		[]string{"monitor", "watch", "--since", "0", "--event-type", "task.created"},
		[]string{"touch", "inbox/follow", "-t", "Follow Task"},
	)

	if normalize(newErr) != normalize(oldErr) {
		t.Fatalf("monitor watch follow stderr mismatch:\n old: %q\n new: %q", oldErr, newErr)
	}
	if got, want := normalizeUUIDString(normalizeMonitorFollowLines(newOut)), normalizeUUIDString(normalizeMonitorFollowLines(oldOut)); got != want {
		t.Fatalf("monitor watch follow stdout mismatch:\n old:\n%s\n new:\n%s", want, got)
	}
}

func TestSearchTTYHumanParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs on a pty")
	}
	bins := buildParityBinaries(t)
	sidecar := filepath.Join(t.TempDir(), "search.sqlite")
	searchEnv := []string{
		"WRKQ_SEARCH_ENABLED=1",
		"WRKQ_SEARCH_DENSE_PROVIDER=none",
		"WRKQ_SEARCH_DB_PATH=" + sidecar,
	}
	base := seedFixtureFilesEnv(t, bins, [][]string{
		{"touch", "inbox/find-me", "-t", "Needle Task", "-d", "needle payload text"},
		{"index", "rebuild"},
	}, nil, searchEnv)
	oldDir := copyFixture(t, base)
	newDir := copyFixture(t, base)

	oldOut, oldExit := runCLIOnTTYEnv(t, bins.wrkq, oldDir, []string{"search", "needle"}, searchEnv)
	newOut, newExit := runCLIOnTTYEnv(t, bins.mirror, newDir, []string{"search", "needle"}, searchEnv)

	if oldExit != newExit {
		t.Fatalf("search TTY exit mismatch: old=%d new=%d\nold=%q\nnew=%q", oldExit, newExit, oldOut, newOut)
	}
	if normalize(newOut) != normalize(oldOut) {
		t.Fatalf("search TTY human mismatch:\n old: %q\n new: %q", oldOut, newOut)
	}
	for _, want := range []string{"search", "needle", "Needle Task", "inbox/find-me"} {
		if !strings.Contains(oldOut, want) {
			t.Fatalf("legacy search TTY output missing %q (got %q)", want, oldOut)
		}
	}
}

func TestWatchTTYHumanSemanticParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + runs both CLIs on a pty")
	}
	bins := buildParityBinaries(t)
	base := seedFixture(t, bins, [][]string{{"touch", "inbox/human", "-t", "Human Watch"}})
	oldDir := copyFixture(t, base)
	newDir := copyFixture(t, base)

	oldOut, oldExit := runCLIOnTTY(t, bins.wrkq, oldDir, []string{"watch", "--follow=false"})
	newOut, newExit := runCLIOnTTY(t, bins.mirror, newDir, []string{"watch", "--follow=false"})

	if oldExit != newExit {
		t.Fatalf("watch TTY exit mismatch: old=%d new=%d\nold=%q\nnew=%q", oldExit, newExit, oldOut, newOut)
	}
	if got, want := watchHumanSemanticSignature(newOut), watchHumanSemanticSignature(oldOut); got != want {
		t.Fatalf("watch TTY semantic mismatch:\n old:\n%s\n new:\n%s", want, got)
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
			wrkq:    build("wrkq-legacy", "./cmd/wrkq-legacy"),
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
	return seedFixtureFiles(t, bins, setup, nil)
}

// seedFixtureFiles is seedFixture plus literal source files written into the
// fixture dir BEFORE the setup commands run, so a seed step that consumes a host
// file path (e.g. `attach put <task> <file>`) has its source present. The seed
// `attach put` writes the attachment file under the seeding binary's default
// (HOME-derived) attach dir; that location is irrelevant to DB-only readers like
// `attach ls`, which compare the stored rows (relative_path is dir-independent).
func seedFixtureFiles(t *testing.T, bins binaries, setup [][]string, files map[string]string) string {
	return seedFixtureFilesEnv(t, bins, setup, files, nil)
}

// seedFixtureFilesEnv is seedFixtureFiles with an extra environment applied to the
// SETUP commands (e.g. WRKQ_ATTACH_DIR so a seed `attach put` stages its bytes
// into a shared absolute attach dir reachable by the command-under-test).
func seedFixtureFilesEnv(t *testing.T, bins binaries, setup [][]string, files map[string]string, seedEnv []string) string {
	t.Helper()
	dir := t.TempDir()
	writeRunFiles(t, dir, files)
	dbPath := filepath.Join(dir, "wrkq.db")
	mustRun(t, bins.wrkqadm, dir, []string{"--db", dbPath, "init"})
	for _, argv := range setup {
		mustRunEnv(t, bins.wrkq, dir, append([]string{"--db", dbPath, "--as", "agent:local-human"}, argv...), seedEnv)
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

// writeRunFiles materializes literal source files into a run dir before the
// command-under-test executes. Used by commands that consume a host file path
// (e.g. `attach put`), which copyFixture (SQLite-only) does not carry.
func writeRunFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if mkErr := os.MkdirAll(filepath.Dir(p), 0o755); mkErr != nil {
			t.Fatalf("write run file mkdir %s: %v", p, mkErr)
		}
		if wErr := os.WriteFile(p, []byte(content), 0o644); wErr != nil {
			t.Fatalf("write run file %s: %v", p, wErr)
		}
	}
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
	return runCLIStdin(t, bin, dir, args, extraEnv, nil)
}

func runCLIStdin(t *testing.T, bin, dir string, args []string, extraEnv []string, stdin []byte) cliResult {
	t.Helper()
	full := append([]string{"--db", filepath.Join(dir, "wrkq.db"), "--as", "agent:local-human"}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Dir = dir
	cmd.Env = append(hermeticEnv(), extraEnv...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
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

func runFollowWithMutation(t *testing.T, bin, dir string, followArgs []string, mutateArgs []string) (stdout, stderr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	full := append([]string{"--db", filepath.Join(dir, "wrkq.db"), "--as", "agent:local-human"}, followArgs...)
	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Dir = dir
	cmd.Env = hermeticEnv()
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Start(); err != nil {
		t.Fatalf("start follow %s %v: %v", bin, followArgs, err)
	}

	time.Sleep(200 * time.Millisecond)
	mut := runCLI(t, bin, dir, mutateArgs)
	if mut.exit != 0 {
		cancel()
		_ = cmd.Wait()
		t.Fatalf("follow mutation %s %v failed: exit=%d stdout=%q stderr=%q", bin, mutateArgs, mut.exit, mut.stdout, mut.stderr)
	}

	waitErr := cmd.Wait()
	if ctx.Err() == nil && waitErr != nil {
		t.Fatalf("follow %s %v exited before timeout: %v stdout=%q stderr=%q", bin, followArgs, waitErr, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "task.created") {
		t.Fatalf("follow %s %v did not observe task.created before timeout; stdout=%q stderr=%q", bin, followArgs, out.String(), errOut.String())
	}
	return out.String(), errOut.String()
}

func mustRun(t *testing.T, bin, dir string, args []string) {
	mustRunEnv(t, bin, dir, args, nil)
}

func mustRunEnv(t *testing.T, bin, dir string, args []string, extraEnv []string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(hermeticEnv(), extraEnv...)
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

func normalizeUUIDString(s string) string {
	return uuidRe.ReplaceAllString(s, "<UUID>")
}

func normalizeWatchFollowLines(s string) string {
	return normalizeFollowLines(s, "task.created")
}

func normalizeMonitorFollowLines(s string) string {
	return normalizeFollowLines(s, "task.created")
}

func normalizeFollowLines(s, eventType string) string {
	var kept []string
	for _, line := range nonEmptyLines(s) {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		if row["event_type"] != eventType {
			continue
		}
		kept = append(kept, normalize(line))
	}
	return strings.Join(kept, "\n")
}

func watchHumanSemanticSignature(out string) string {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	var sig []string
	for _, line := range nonEmptyLines(out) {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "wrkq watch is deprecated"):
			sig = append(sig, "warning:"+trimmed)
		case strings.HasPrefix(trimmed, "["):
			sig = append(sig, "event:"+normalizeUUIDString(normalize(trimmed)))
		case strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}"):
			sig = append(sig, "payload:"+watchPayloadSignature(trimmed))
		default:
			sig = append(sig, "line:"+normalizeUUIDString(normalize(trimmed)))
		}
	}
	return strings.Join(sig, "\n")
}

func watchPayloadSignature(payload string) string {
	payload = strings.TrimPrefix(strings.TrimSuffix(payload, "}"), "{")
	if strings.TrimSpace(payload) == "" {
		return "{}"
	}
	parts := strings.Split(payload, ", ")
	for i := range parts {
		parts[i] = normalizeUUIDString(normalize(parts[i]))
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ", ") + "}"
}

// snapshot renders the durable task state as a stable string: id, slug, state,
// priority, kind, project/routing/linkage/text/meta/date fields, etag, and whether acknowledged/completed timestamps are set
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
		SELECT id, slug, state, priority, kind,
		       project_uuid,
		       COALESCE(parent_task_uuid, ''), COALESCE(assignee_principal_ref, ''),
		       COALESCE(description, ''), COALESCE(specification, ''),
		       COALESCE(labels, ''), COALESCE(meta, ''),
		       COALESCE(requested_by_project_id, ''), COALESCE(assigned_project_id, ''),
		       COALESCE(resolution, ''),
		       COALESCE(start_at, ''), COALESCE(due_at, ''),
		       etag,
		       CASE WHEN acknowledged_at IS NOT NULL AND acknowledged_at != '' THEN 'ack' ELSE '-' END,
		       CASE WHEN completed_at    IS NOT NULL AND completed_at    != '' THEN 'done' ELSE '-' END
		FROM tasks ORDER BY id`)
	if err != nil {
		t.Fatalf("snapshot query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var id, slug, state, kind, projectUUID, description, specification, labels, meta string
		var parentTaskUUID, assignee, requestedBy, assignedProject, resolution string
		var startAt, dueAt, ackd, done string
		var prio, etag int
		if err := rows.Scan(&id, &slug, &state, &prio, &kind, &projectUUID, &parentTaskUUID, &assignee, &description, &specification, &labels, &meta, &requestedBy, &assignedProject, &resolution, &startAt, &dueAt, &etag, &ackd, &done); err != nil {
			t.Fatalf("snapshot scan: %v", err)
		}
		b.WriteString("task|" + strings.Join([]string{
			id, slug, state, strconv.Itoa(prio), kind,
			projectUUID,
			parentTaskUUID, assignee,
			description, specification, labels, meta, startAt, dueAt,
			requestedBy, assignedProject, resolution,
			strconv.Itoa(etag), ackd, done,
		}, "|"))
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshot rows: %v", err)
	}

	// webhook_urls is included so the DEDICATED webhook surfaces prove DURABLE
	// write parity. archived_at presence + etag make container rm/archive parity
	// durable, not just rendered. updated_by_principal_ref proves caller-resolved
	// write ATTRIBUTION (daedalus #10261).
	crows, err := database.Query(`
		SELECT id, slug, kind, COALESCE(parent_uuid, ''), COALESCE(webhook_urls, ''),
		       CASE WHEN archived_at IS NOT NULL AND archived_at != '' THEN 'arch' ELSE '-' END,
		       etag, COALESCE(updated_by_principal_ref, '')
		FROM containers ORDER BY id`)
	if err != nil {
		t.Fatalf("snapshot container query: %v", err)
	}
	defer func() { _ = crows.Close() }()
	for crows.Next() {
		var id, slug, kind, parentUUID, hooks, archived, updatedBy string
		var etag int
		if err := crows.Scan(&id, &slug, &kind, &parentUUID, &hooks, &archived, &etag, &updatedBy); err != nil {
			t.Fatalf("snapshot container scan: %v", err)
		}
		b.WriteString("container|" + strings.Join([]string{id, slug, kind, parentUUID, hooks, archived, strconv.Itoa(etag), updatedBy}, "|") + "\n")
	}
	if err := crows.Err(); err != nil {
		t.Fatalf("snapshot container rows: %v", err)
	}

	// Comments: id, deleted presence (not the wall-clock value), and etag. Lets
	// `comment rm` (soft-delete bumps etag + sets deleted_at; purge removes the
	// row) prove durable parity, not just rendered output.
	mrows, err := database.Query(`
		SELECT id, etag,
		       CASE WHEN deleted_at IS NOT NULL AND deleted_at != '' THEN 'del' ELSE '-' END
		FROM comments ORDER BY id`)
	if err != nil {
		t.Fatalf("snapshot comment query: %v", err)
	}
	defer func() { _ = mrows.Close() }()
	for mrows.Next() {
		var id, deleted string
		var etag int
		if err := mrows.Scan(&id, &etag, &deleted); err != nil {
			t.Fatalf("snapshot comment scan: %v", err)
		}
		b.WriteString("comment|" + strings.Join([]string{id, strconv.Itoa(etag), deleted}, "|") + "\n")
	}
	if err := mrows.Err(); err != nil {
		t.Fatalf("snapshot comment rows: %v", err)
	}
	return b.String()
}
