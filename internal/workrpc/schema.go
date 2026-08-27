package workrpc

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/lherron/wrkq/internal/wrkqapi"
)

var dtoSchemaTypes = map[string]reflect.Type{
	"RPCInitializeResult":           dtoType[initializeResult](),
	"WrkqTask":                      dtoType[wrkqapi.WrkqTask](),
	"WrkqTaskCopyParams":            dtoType[wrkqapi.TaskCopyParams](),
	"WrkqTaskCopyResult":            dtoType[wrkqapi.WrkqTaskCopyResult](),
	"WrkqTaskCatView":               dtoType[wrkqapi.WrkqTaskCatView](),
	"WrkqTaskClaim":                 dtoType[wrkqapi.WrkqTaskClaim](),
	"WrkqPromise":                   dtoType[wrkqapi.WrkqPromise](),
	"WrkqPromiseSubjectRef":         dtoType[wrkqapi.WrkqPromiseSubjectRef](),
	"WrkqPromiseListResult":         dtoType[wrkqapi.WrkqPromiseListResult](),
	"WrkqPromiseAddParams":          dtoType[wrkqapi.PromiseAddParams](),
	"WrkqPromiseShowParams":         dtoType[wrkqapi.PromiseShowParams](),
	"WrkqPromiseListParams":         dtoType[wrkqapi.PromiseListParams](),
	"WrkqPromiseReadyParams":        dtoType[wrkqapi.PromiseReadyParams](),
	"WrkqPromiseEditParams":         dtoType[wrkqapi.PromiseEditParams](),
	"WrkqPromiseReviewParams":       dtoType[wrkqapi.PromiseReviewParams](),
	"WrkqPromiseRetargetParams":     dtoType[wrkqapi.PromiseRetargetParams](),
	"WrkqPromiseDeleteParams":       dtoType[wrkqapi.PromiseDeleteParams](),
	"WrkqRoom":                      dtoType[wrkqapi.WrkqRoom](),
	"WrkqRoomWorkRef":               dtoType[wrkqapi.WrkqRoomWorkRef](),
	"WrkqRoomLink":                  dtoType[wrkqapi.WrkqRoomLink](),
	"WrkqRoomListResult":            dtoType[wrkqapi.WrkqRoomListResult](),
	"WrkqRoomLogView":               dtoType[wrkqapi.WrkqRoomLogView](),
	"WrkqRoomMember":                dtoType[wrkqapi.WrkqRoomMember](),
	"WrkqRoomMembersView":           dtoType[wrkqapi.WrkqRoomMembersView](),
	"WrkqRoomSayResult":             dtoType[wrkqapi.WrkqRoomSayResult](),
	"WrkqEnvelope":                  dtoType[wrkqapi.WrkqEnvelope](),
	"WrkqEnvelopeParty":             dtoType[wrkqapi.WrkqEnvelopeParty](),
	"WrkqEnvelopePresentation":      dtoType[wrkqapi.WrkqEnvelopePresentation](),
	"WrkqEnvelopeInboxGroup":        dtoType[wrkqapi.WrkqEnvelopeInboxGroup](),
	"WrkqEnvelopeInboxView":         dtoType[wrkqapi.WrkqEnvelopeInboxView](),
	"WrkqEnvelopePresentResult":     dtoType[wrkqapi.WrkqEnvelopePresentResult](),
	"WrkqEnvelopePendingView":       dtoType[wrkqapi.WrkqEnvelopePendingView](),
	"WrkqRoomSayParams":             dtoType[wrkqapi.RoomSayParams](),
	"WrkqRoomOpenParams":            dtoType[wrkqapi.RoomOpenParams](),
	"WrkqRoomShowParams":            dtoType[wrkqapi.RoomShowParams](),
	"WrkqRoomListParams":            dtoType[wrkqapi.RoomListParams](),
	"WrkqRoomLogViewParams":         dtoType[wrkqapi.RoomLogViewParams](),
	"WrkqRoomLifecycleParams":       dtoType[wrkqapi.RoomLifecycleParams](),
	"WrkqRoomMemberParams":          dtoType[wrkqapi.RoomMemberParams](),
	"WrkqRoomMembersViewParams":     dtoType[wrkqapi.RoomMembersViewParams](),
	"WrkqEnvelopeShowParams":        dtoType[wrkqapi.EnvelopeShowParams](),
	"WrkqEnvelopeInboxViewParams":   dtoType[wrkqapi.EnvelopeInboxViewParams](),
	"WrkqEnvelopeDeferParams":       dtoType[wrkqapi.EnvelopeDeferParams](),
	"WrkqEnvelopeAckParams":         dtoType[wrkqapi.EnvelopeAckParams](),
	"WrkqEnvelopePresentParams":     dtoType[wrkqapi.EnvelopePresentParams](),
	"WrkqEnvelopePendingViewParams": dtoType[wrkqapi.EnvelopePendingViewParams](),
	"WrkqEnvelopeRoundParams":       dtoType[wrkqapi.EnvelopeRoundParams](),
	"WrkqContainerCatView":          dtoType[wrkqapi.WrkqContainerCatView](),
	"WrkqCommentCatView":            dtoType[wrkqapi.WrkqCommentCatView](),
	"WrkqCommentListView":           dtoType[wrkqapi.WrkqCommentListView](),
	"WrkqAttachmentListView":        dtoType[wrkqapi.WrkqAttachmentListView](),
	"WrkqLsListView":                dtoType[wrkqapi.WrkqLsListView](),
	"WrkqFindListViewParams":        dtoType[wrkqapi.FindListViewParams](),
	"WrkqFindListView":              dtoType[wrkqapi.WrkqFindListView](),
	"WrkqHistoryListView":           dtoType[wrkqapi.WrkqHistoryListView](),
	"WrkqLogEvent":                  dtoType[wrkqapi.WrkqLogEvent](),
	"WrkqHistoryTailView":           dtoType[wrkqapi.WrkqHistoryTailView](),
	"WrkqWatchEvent":                dtoType[wrkqapi.WrkqWatchEvent](),
	"WrkqMonitorEventsView":         dtoType[wrkqapi.WrkqMonitorEventsView](),
	"WrkqMonitorEvent":              dtoType[wrkqapi.WrkqMonitorEvent](),
	"WrkqMonitorStateView":          dtoType[wrkqapi.WrkqMonitorStateView](),
	"WrkqTreeView":                  dtoType[wrkqapi.WrkqTreeView](),
	"WrkqTaskBlockedView":           dtoType[wrkqapi.WrkqTaskBlockedView](),
	"WrkqInboxView":                 dtoType[wrkqapi.WrkqInboxView](),
	"CatViewRelation":               dtoType[wrkqapi.CatViewRelation](),
	"WrkqTaskListResult":            dtoType[wrkqapi.WrkqTaskListResult](),
	"WrkqComment":                   dtoType[wrkqapi.WrkqComment](),
	"WrkqCommentListResult":         dtoType[wrkqapi.WrkqCommentListResult](),
	"WrkqAttachment":                dtoType[wrkqapi.WrkqAttachment](),
	"WrkqAttachmentBytes":           dtoType[wrkqapi.WrkqAttachmentBytes](),
	"WrkqAttachmentAddBytesResult":  dtoType[wrkqapi.WrkqAttachmentAddBytesResult](),
	"WrkqRelation":                  dtoType[wrkqapi.WrkqRelation](),
	"WrkqContainer":                 dtoType[wrkqapi.WrkqContainer](),
	"WrkqContainerListResult":       dtoType[wrkqapi.WrkqContainerListResult](),
	"WrkqContainerTaskCount":        dtoType[wrkqapi.WrkqContainerTaskCount](),
	"WrkqContainerTaskCounts":       dtoType[wrkqapi.WrkqContainerTaskCounts](),
	"WrkqCampaignMemberDiagnostic":  dtoType[wrkqapi.WrkqCampaignMemberDiagnostic](),
	"WrkqCampaignTransitionResult":  dtoType[wrkqapi.WrkqCampaignTransitionResult](),
	"WrkqCampaignProject":           dtoType[wrkqapi.WrkqCampaignProject](),
	"WrkqCampaignFootprint":         dtoType[wrkqapi.WrkqCampaignFootprint](),
	"WrkqCampaignPortfolioRow":      dtoType[wrkqapi.WrkqCampaignPortfolioRow](),
	"WrkqCampaignPortfolio":         dtoType[wrkqapi.WrkqCampaignPortfolio](),
	"WrkqTimelineContainer":         dtoType[wrkqapi.WrkqTimelineContainer](),
	"WrkqCampaignAdornment":         dtoType[wrkqapi.WrkqCampaignAdornment](),
	"WrkqTimelineMember":            dtoType[wrkqapi.WrkqTimelineMember](),
	"WrkqTimelineRollup":            dtoType[wrkqapi.WrkqTimelineRollup](),
	"WrkqTimelineComment":           dtoType[wrkqapi.WrkqTimelineComment](),
	"WrkqTimelineOutcome":           dtoType[wrkqapi.WrkqTimelineOutcome](),
	"WrkqTimelineTaskState":         dtoType[wrkqapi.WrkqTimelineTaskState](),
	"WrkqTimelineContainerState":    dtoType[wrkqapi.WrkqTimelineContainerState](),
	"WrkqTimelineEntry":             dtoType[wrkqapi.WrkqTimelineEntry](),
	"WrkqContainerTimelineView":     dtoType[wrkqapi.WrkqContainerTimelineView](),
	"WrkqProjectsListView":          dtoType[wrkqapi.WrkqProjectsListView](),
	"WrkqProjectEntry":              dtoType[wrkqapi.WrkqProjectEntry](),
	"WebhookRow":                    dtoType[wrkqapi.WebhookRow](),
	"WrkqWorkflowAttachResult":      dtoType[wrkqapi.WrkqWorkflowAttachResult](),
	"WrkqWorkflowInspectResult":     dtoType[wrkqapi.WrkqWorkflowInspectResult](),
	"WrkqWorkflowInstancesResult":   dtoType[wrkqapi.WrkqWorkflowInstancesResult](),
	"WrkqWorkflowSyncMetaResult":    dtoType[wrkqapi.WrkqWorkflowSyncMetaResult](),
	"WrkqHandoff":                   dtoType[wrkqapi.WrkqHandoff](),
	"WrkqHandoffCreateResult":       dtoType[wrkqapi.WrkqHandoffCreateResult](),
	"WrkqHandoffListResult":         dtoType[wrkqapi.WrkqHandoffListResult](),
	"WrkqHandoffSearchResult":       dtoType[wrkqapi.WrkqHandoffSearchResult](),
	"WrkqSearchListViewParams":      dtoType[wrkqapi.SearchListViewParams](),
	"WrkqSearchListView":            dtoType[wrkqapi.WrkqSearchListView](),
	"WrkqSearchResult":              dtoType[wrkqapi.WrkqSearchResult](),
	"WrkqIndexStatus":               dtoType[wrkqapi.WrkqIndexStatus](),
	"WrkfInstance":                  dtoType[wrkfapi.Instance](),
	"WrkfEvent":                     dtoType[wrkfapi.Event](),
	"WrkfEventQueryResult":          dtoType[wrkfapi.EventQueryResult](),
	"WrkfQueriedEvent":              dtoType[wrkfapi.QueriedEvent](),
	"WrkfEvidence":                  dtoType[wrkfapi.Evidence](),
	"WrkfEvidenceSchema":            dtoType[wrkfapi.EvidenceSchema](),
	"WrkfLedgerEntry":               dtoType[wrkfapi.LedgerEntry](),
	"WrkfLedgerListResult":          dtoType[wrkfapi.LedgerListResult](),
	"WrkfObligation":                dtoType[wrkfapi.Obligation](),
	"WrkfEffect":                    dtoType[wrkfapi.Effect](),
	"WrkfRun":                       dtoType[wrkfapi.Run](),
	"WrkfCheckRun":                  dtoType[wrkfapi.CheckRun](),
	"WrkfTransitionResult":          dtoType[wrkfapi.TransitionResult](),
	"WrkfSuspensionResolveResult":   dtoType[wrkfapi.SuspensionResolveResult](),
	"WrkfInstanceCancelResult":      dtoType[wrkfapi.InstanceCancelResult](),
	"WrkfWorkflowTemplateSummary":   dtoType[wrkfapi.TemplateSummary](),
	"WrkfWorkflowContentParams":     dtoType[wrkfapi.WorkflowContentParams](),
	"WrkfWorkflowDiffParams":        dtoType[wrkfapi.WorkflowDiffParams](),
	"WrkfWorkflowInstallParams":     dtoType[wrkfapi.WorkflowInstallParams](),
	"WrkfWorkflowListResult":        dtoType[wrkfapi.WorkflowListResult](),
	"WrkfWorkflowShowResult":        dtoType[wrkfapi.WorkflowShowResult](),
	"WrkfInstallResult":             dtoType[wrkfapi.InstallResult](),
	"WrkfDiffResult":                dtoType[wrkfapi.DiffResult](),
	"WrkfSuggestResult":             dtoType[wrkfapi.SuggestResult](),
	"WrkfEffectClaimResult":         dtoType[wrkfapi.EffectClaim](),
	"WrkfActionClaimPredecessor":    dtoType[wrkfapi.ActionClaimPredecessor](),
	"WrkfWatchSnapshot":             dtoType[wrkfapi.WatchSnapshot](),
	"WrkfWatchEventsResult":         dtoType[wrkfapi.WatchEventsResult](),
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
