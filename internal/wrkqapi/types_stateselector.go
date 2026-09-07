package wrkqapi

import (
	"sort"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
)

// stateSelectorAny is the literal that selects every task state. It is a
// distinct token rather than an empty list, because an empty list is a caller
// mistake (T-08216 §5.3) and must not silently widen the read.
const stateSelectorAny = "any"

// Lifecycle values for LifecycleSelector. Row lifecycle is orthogonal to task
// state: a row may carry archived_at/deleted_at in ANY state, so the two are
// selected independently rather than through one overloaded boolean.
const (
	lifecycleLive     = "live"
	lifecycleArchived = "archived"
	lifecycleDeleted  = "deleted"
	lifecycleAny      = "any"
)

// stateSelector is the resolved, validated form of a caller's `states` request.
// The server owns the walk; the caller owns which states that walk may show
// (T-08216). There is deliberately NO default here — an absent selector is a
// validation error, never a server-chosen set, so that a caller whose parameter
// was dropped in transit is refused rather than silently answered with a
// different question.
type stateSelector struct {
	all    bool
	states map[string]bool
}

func (s stateSelector) includes(state string) bool {
	if s.all {
		return true
	}
	return s.states[state]
}

// resolveStateSelector validates a `states` parameter. field names the wire
// parameter in the error so a caller learns which key it got wrong.
func resolveStateSelector(raw flexString, field string) (stateSelector, error) {
	if len(raw) == 0 {
		return stateSelector{}, NewValidationError(
			field+" is required: pass a state list (e.g. [\"draft\",\"open\",\"in_progress\"]) or \""+stateSelectorAny+"\"",
			map[string]any{"field": field})
	}
	sel := stateSelector{states: make(map[string]bool, len(raw))}
	for _, v := range raw {
		v = strings.TrimSpace(v)
		if v == stateSelectorAny {
			sel.all = true
			continue
		}
		if !domain.IsValidState(v) {
			return stateSelector{}, NewValidationError(
				"unknown task state "+v+" in "+field+": valid states are "+strings.Join(domain.AllStates(), ", ")+" (or \""+stateSelectorAny+"\")",
				map[string]any{"field": field, "value": v})
		}
		sel.states[v] = true
	}
	if !sel.all && len(sel.states) == 0 {
		return stateSelector{}, NewValidationError(
			field+" is required: pass a state list or \""+stateSelectorAny+"\"",
			map[string]any{"field": field})
	}
	return sel, nil
}

// resolveLifecycle validates a `lifecycle` parameter, defaulting to live.
func resolveLifecycle(raw string, field string) (string, error) {
	switch strings.TrimSpace(raw) {
	case "":
		return lifecycleLive, nil
	case lifecycleLive:
		return lifecycleLive, nil
	case lifecycleArchived:
		return lifecycleArchived, nil
	case lifecycleDeleted:
		return lifecycleDeleted, nil
	case lifecycleAny:
		return lifecycleAny, nil
	default:
		return "", NewValidationError(
			field+" must be one of live, archived, deleted, any",
			map[string]any{"field": field, "value": raw})
	}
}

// lifecycleAdmits reports whether a row with these lifecycle stamps is selected.
func lifecycleAdmits(lifecycle string, isArchived, isDeleted bool) bool {
	switch lifecycle {
	case lifecycleAny:
		return true
	case lifecycleArchived:
		return isArchived
	case lifecycleDeleted:
		return isDeleted
	default: // live
		return !isArchived && !isDeleted
	}
}

// treeFilter is the resolved visibility request threaded through the tree and
// ls walks: which task states the caller asked to see, and which row lifecycle.
// It replaces the includeArchived/openOnly boolean pair, which conflated state
// selection, row lifecycle and empty-container pruning into two flags.
type treeFilter struct {
	states    stateSelector
	lifecycle string
}

// admits reports whether a task row is selected by this filter.
func (f treeFilter) admits(state string, isArchived, isDeleted bool) bool {
	return f.states.includes(state) && lifecycleAdmits(f.lifecycle, isArchived, isDeleted)
}

// includesArchivedContainers reports whether archived CONTAINER rows should be
// listed. Containers have no state, so only lifecycle governs them.
func (f treeFilter) includesArchivedContainers() bool {
	return f.lifecycle != lifecycleLive
}

// sqlPredicate renders the filter as a SQL fragment over a task table alias
// (pass "" for an unaliased column reference). It returns the clause and its
// bind args. An "any" state selector contributes no state clause, so the
// generated SQL stays as close to the pre-T-08216 shape as the request allows.
func (f treeFilter) sqlPredicate(alias string) (string, []any) {
	col := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}
	var clause strings.Builder
	var args []any
	if !f.states.all {
		names := make([]string, 0, len(f.states.states))
		for name := range f.states.states {
			names = append(names, name)
		}
		sort.Strings(names) // deterministic SQL for identical requests
		clause.WriteString(" AND " + col("state") + " IN (")
		for i, name := range names {
			if i > 0 {
				clause.WriteString(", ")
			}
			clause.WriteString("?")
			args = append(args, name)
		}
		clause.WriteString(")")
	}
	switch f.lifecycle {
	case lifecycleAny:
		// no lifecycle predicate
	case lifecycleArchived:
		clause.WriteString(" AND " + col("archived_at") + " IS NOT NULL")
	case lifecycleDeleted:
		clause.WriteString(" AND " + col("deleted_at") + " IS NOT NULL")
	default: // live
		clause.WriteString(" AND " + col("archived_at") + " IS NULL AND " + col("deleted_at") + " IS NULL")
	}
	return clause.String(), args
}
