#!/usr/bin/env sh
# Canonical Go build tags for the local (SQLite-linked) wrkq build.
#
# The single source for `just build` and friends, scripts/agent-check.sh, and
# the test/*.sh smokes, so a new tag cannot silently break one of them.
# .golangci.yml cannot read this script and lists the same tags literally.
printf '%s\n' "sqlite_fts5,wrkq_local"
