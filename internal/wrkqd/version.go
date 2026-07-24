package wrkqd

// Build metadata is injected by scripts/ldflags.sh for shipped wrkqd binaries.
// Version intentionally retains git describe's "-dirty" suffix so Revision is
// never mistaken for an exact clean source pin when the binary was built from a
// dirty worktree.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)
