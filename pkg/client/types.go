package client

import "github.com/lherron/wrkq/internal/wrkqapi"

// Public resource DTOs are aliases of the server's canonical wire structs, so
// their JSON fields cannot drift into a second client-side protocol copy.
type SubtaskSummary = wrkqapi.SubtaskSummary
type TaskCreateParams = wrkqapi.TaskCreateParams
type TaskCovers = wrkqapi.TaskCovers
type Task = wrkqapi.WrkqTask
type TaskPatch = wrkqapi.TaskPatch
type TaskListResult = wrkqapi.WrkqTaskListResult
type Comment = wrkqapi.WrkqComment
type Promise = wrkqapi.WrkqPromise
type PromiseListResult = wrkqapi.WrkqPromiseListResult
type PromiseSubjectRef = wrkqapi.WrkqPromiseSubjectRef
type Container = wrkqapi.WrkqContainer
type Room = wrkqapi.WrkqRoom
type RoomWorkRef = wrkqapi.WrkqRoomWorkRef
type RoomLink = wrkqapi.WrkqRoomLink
type Envelope = wrkqapi.WrkqEnvelope
type EnvelopeParty = wrkqapi.WrkqEnvelopeParty
type EnvelopePresentation = wrkqapi.WrkqEnvelopePresentation
type EnvelopeInboxView = wrkqapi.WrkqEnvelopeInboxView
type EnvelopeInboxGroup = wrkqapi.WrkqEnvelopeInboxGroup
type EnvelopeWithdrawResult = wrkqapi.WrkqEnvelopeWithdrawResult
type EnvelopeWithdrawRefusal = wrkqapi.WrkqEnvelopeWithdrawRefusal
type RoomLog = wrkqapi.WrkqRoomLogView

// History event reads expose exact resource identity and the event's own task ID.
type HistoryTailViewParams = wrkqapi.HistoryTailViewParams
type HistoryTailView = wrkqapi.WrkqHistoryTailView
type HistoryListViewParams = wrkqapi.HistoryListViewParams
type HistoryListView = wrkqapi.WrkqHistoryListView
type HistoryEvent = wrkqapi.WrkqLogEvent
type MonitorEvent = wrkqapi.WrkqMonitorEvent

// Extensions retain their own fence and public subject identity.
type ExtSubject = wrkqapi.ExtSubject
type Ext = wrkqapi.WrkqExt
type ExtSubjectRef = wrkqapi.ExtSubjectRef
type ExtListItem = wrkqapi.WrkqExtListItem
type ExtPutParams = wrkqapi.ExtPutParams
type ExtPutResult = wrkqapi.ExtPutResult
type ExtShowParams = wrkqapi.ExtShowParams
type ExtShowResult = wrkqapi.ExtShowResult
type ExtListViewParams = wrkqapi.ExtListViewParams
type ExtListView = wrkqapi.WrkqExtListView
type ExtDeleteParams = wrkqapi.ExtDeleteParams
type ExtDeleteResult = wrkqapi.ExtDeleteResult
