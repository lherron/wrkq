package rpccli

import "strings"

// Not-found hints for the wrkq, wrkc and wrkp binaries (CLI standard §4): the
// valid selector forms for the missing kind and the command that lists real
// candidates, in each binary's own vocabulary. clifunnel appends the hint after
// the unchanged "<kind> not found: <ref>" line.

func wrkqNotFoundHint(kind, _ string) string {
	switch strings.ToLower(kind) {
	case "task", "parent task", "subtask owner":
		return "a task is T-<n>, its uuid, or a path like inbox/<slug>; completed, archived and deleted tasks are hidden by default. Search with: wrkq find --state all --type t  or  wrkq search '<words>' --state all"
	case "container", "campaign", "project", "container task count":
		return "a container is P-<n>, its uuid, or a path in the current project (a leading / is root-absolute). List what exists with: wrkq tree  (all projects: wrkq projects)"
	case "", "path", "target", "source":
		return "a selector is a task or container: T-<n>, P-<n>, a uuid, or a path in the current project (a leading / is root-absolute). Browse with: wrkq tree  or search tasks with: wrkq find --state all"
	case "comment":
		return "a comment is C-<n>. List a task's comments with: wrkq comment ls <task>"
	case "promise":
		return "a promise is PR-<n>. List them with: wrkq promise list"
	case "attachment":
		return "an attachment is ATT-<n>. List a task's attachments with: wrkq attach ls <task>"
	}
	return ""
}

func wrkcNotFoundHint(kind, ref string) string {
	switch strings.ToLower(kind) {
	case "room":
		return "a room is R-<n>, a task or container ref, or agent@project. List rooms with: wrkc ls"
	case "envelope":
		return "an envelope is EN-<n>. Mail addressed to you: wrkc inbox; a room's history: wrkc log <room>"
	}
	return wrkqNotFoundHint(kind, ref)
}

func wrkpNotFoundHint(kind, ref string) string {
	switch strings.ToLower(kind) {
	case "project event":
		return "an event is PE-<n>. Read the project timeline with: wrkp log --project <project>"
	}
	return wrkqNotFoundHint(kind, ref)
}
