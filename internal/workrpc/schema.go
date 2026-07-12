package workrpc

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/lherron/wrkq/internal/wrkqapi"
)

var dtoSchemaTypes = map[string]reflect.Type{
	"WrkqTask":                     dtoType[wrkqapi.WrkqTask](),
	"WrkqTaskCopyParams":           dtoType[wrkqapi.TaskCopyParams](),
	"WrkqTaskCopyResult":           dtoType[wrkqapi.WrkqTaskCopyResult](),
	"WrkqTaskCatView":              dtoType[wrkqapi.WrkqTaskCatView](),
	"WrkqContainerCatView":         dtoType[wrkqapi.WrkqContainerCatView](),
	"WrkqCommentCatView":           dtoType[wrkqapi.WrkqCommentCatView](),
	"WrkqCommentListView":          dtoType[wrkqapi.WrkqCommentListView](),
	"WrkqAttachmentListView":       dtoType[wrkqapi.WrkqAttachmentListView](),
	"WrkqLsListView":               dtoType[wrkqapi.WrkqLsListView](),
	"WrkqFindListView":             dtoType[wrkqapi.WrkqFindListView](),
	"WrkqHistoryListView":          dtoType[wrkqapi.WrkqHistoryListView](),
	"WrkqLogEvent":                 dtoType[wrkqapi.WrkqLogEvent](),
	"WrkqHistoryTailView":          dtoType[wrkqapi.WrkqHistoryTailView](),
	"WrkqWatchEvent":               dtoType[wrkqapi.WrkqWatchEvent](),
	"WrkqMonitorEventsView":        dtoType[wrkqapi.WrkqMonitorEventsView](),
	"WrkqMonitorEvent":             dtoType[wrkqapi.WrkqMonitorEvent](),
	"WrkqMonitorStateView":         dtoType[wrkqapi.WrkqMonitorStateView](),
	"WrkqTreeView":                 dtoType[wrkqapi.WrkqTreeView](),
	"WrkqTaskBlockedView":          dtoType[wrkqapi.WrkqTaskBlockedView](),
	"WrkqInboxView":                dtoType[wrkqapi.WrkqInboxView](),
	"CatViewRelation":              dtoType[wrkqapi.CatViewRelation](),
	"WrkqTaskListResult":           dtoType[wrkqapi.WrkqTaskListResult](),
	"WrkqComment":                  dtoType[wrkqapi.WrkqComment](),
	"WrkqCommentListResult":        dtoType[wrkqapi.WrkqCommentListResult](),
	"WrkqAttachment":               dtoType[wrkqapi.WrkqAttachment](),
	"WrkqAttachmentBytes":          dtoType[wrkqapi.WrkqAttachmentBytes](),
	"WrkqAttachmentAddBytesResult": dtoType[wrkqapi.WrkqAttachmentAddBytesResult](),
	"WrkqRelation":                 dtoType[wrkqapi.WrkqRelation](),
	"WrkqContainer":                dtoType[wrkqapi.WrkqContainer](),
	"WrkqContainerListResult":      dtoType[wrkqapi.WrkqContainerListResult](),
	"WrkqProjectsListView":         dtoType[wrkqapi.WrkqProjectsListView](),
	"WrkqProjectEntry":             dtoType[wrkqapi.WrkqProjectEntry](),
	"WebhookRow":                   dtoType[wrkqapi.WebhookRow](),
	"WrkqWorkflowAttachResult":     dtoType[wrkqapi.WrkqWorkflowAttachResult](),
	"WrkqWorkflowInspectResult":    dtoType[wrkqapi.WrkqWorkflowInspectResult](),
	"WrkqHandoff":                  dtoType[wrkqapi.WrkqHandoff](),
	"WrkqHandoffCreateResult":      dtoType[wrkqapi.WrkqHandoffCreateResult](),
	"WrkqHandoffListResult":        dtoType[wrkqapi.WrkqHandoffListResult](),
	"WrkqHandoffSearchResult":      dtoType[wrkqapi.WrkqHandoffSearchResult](),
	"WrkqSearchListView":           dtoType[wrkqapi.WrkqSearchListView](),
	"WrkqSearchResult":             dtoType[wrkqapi.WrkqSearchResult](),
	"WrkqIndexStatus":              dtoType[wrkqapi.WrkqIndexStatus](),
	"WrkfInstance":                 dtoType[wrkfapi.Instance](),
	"WrkfEvent":                    dtoType[wrkfapi.Event](),
	"WrkfEventQueryResult":         dtoType[wrkfapi.EventQueryResult](),
	"WrkfTransitionEvent":          dtoType[wrkfapi.TransitionEvent](),
	"WrkfEvidence":                 dtoType[wrkfapi.Evidence](),
	"WrkfLedgerEntry":              dtoType[wrkfapi.LedgerEntry](),
	"WrkfLedgerListResult":         dtoType[wrkfapi.LedgerListResult](),
	"WrkfObligation":               dtoType[wrkfapi.Obligation](),
	"WrkfEffect":                   dtoType[wrkfapi.Effect](),
	"WrkfRun":                      dtoType[wrkfapi.Run](),
	"WrkfCheckRun":                 dtoType[wrkfapi.CheckRun](),
	"WrkfTransitionResult":         dtoType[wrkfapi.TransitionResult](),
	"WrkfWorkflowTemplateSummary":  dtoType[wrkfapi.TemplateSummary](),
	"WrkfWorkflowListResult":       dtoType[wrkfapi.WorkflowListResult](),
	"WrkfWorkflowShowResult":       dtoType[wrkfapi.WorkflowShowResult](),
	"WrkfInstallResult":            dtoType[wrkfapi.InstallResult](),
	"WrkfDiffResult":               dtoType[wrkfapi.DiffResult](),
	"WrkfSuggestResult":            dtoType[wrkfapi.SuggestResult](),
	"WrkfEffectClaimResult":        dtoType[wrkfapi.EffectClaim](),
}

func dtoType[T any]() reflect.Type {
	var zero T
	return reflect.TypeOf(zero)
}

func dtoSchemaFingerprint(name string, typ reflect.Type) string {
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('=')
	writeSchemaType(&b, typ, map[reflect.Type]bool{})
	return b.String()
}

func writeSchemaType(b *strings.Builder, typ reflect.Type, seen map[reflect.Type]bool) {
	if typ == nil {
		b.WriteString("<nil>")
		return
	}
	for typ.Kind() == reflect.Pointer {
		b.WriteByte('*')
		typ = typ.Elem()
	}

	switch typ.Kind() {
	case reflect.Struct:
		if !isInternalStruct(typ) {
			b.WriteString(typ.String())
			return
		}
		if seen[typ] {
			b.WriteString(typ.String())
			b.WriteString("<recursive>")
			return
		}
		seen[typ] = true
		defer delete(seen, typ)

		b.WriteString(typ.String())
		b.WriteByte('{')
		first := true
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.PkgPath != "" {
				continue
			}
			if !first {
				b.WriteByte(';')
			}
			first = false
			b.WriteString(field.Name)
			b.WriteByte(':')
			b.WriteString(field.Tag.Get("json"))
			b.WriteByte(':')
			writeSchemaType(b, field.Type, seen)
		}
		b.WriteByte('}')
	case reflect.Slice, reflect.Array:
		b.WriteString(typ.Kind().String())
		b.WriteByte('[')
		writeSchemaType(b, typ.Elem(), seen)
		b.WriteByte(']')
	case reflect.Map:
		b.WriteString("map[")
		writeSchemaType(b, typ.Key(), seen)
		b.WriteByte(']')
		writeSchemaType(b, typ.Elem(), seen)
	case reflect.Interface:
		if typ.NumMethod() == 0 {
			b.WriteString("interface{}")
			return
		}
		b.WriteString(typ.String())
	default:
		_, _ = fmt.Fprintf(b, "%s:%s", typ.Kind(), typ.String())
	}
}

func isInternalStruct(typ reflect.Type) bool {
	return strings.HasPrefix(typ.PkgPath(), "github.com/lherron/wrkq/internal/")
}
