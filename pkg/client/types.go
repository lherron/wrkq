package client

import "github.com/lherron/wrkq/internal/wrkqapi"

// Public resource DTOs are aliases of the server's canonical wire structs, so
// their JSON fields cannot drift into a second client-side protocol copy.
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
type RoomLog = wrkqapi.WrkqRoomLogView
