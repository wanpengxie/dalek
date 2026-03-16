package store

import "dalek/internal/contracts"

type Ticket = contracts.Ticket
type Worker = contracts.Worker
type InboxItem = contracts.InboxItem
type MergeItem = contracts.MergeItem
type TicketWorkflowEvent = contracts.TicketWorkflowEvent
type WorkerStatusEvent = contracts.WorkerStatusEvent

type PMState = contracts.PMState
type PMDispatchJob = contracts.PMDispatchJob
type TaskRun = contracts.TaskRun
type RunView = contracts.RunView
type Snapshot = contracts.Snapshot
type Node = contracts.Node
type WorkspaceAssignment = contracts.WorkspaceAssignment
type SubagentRun = contracts.SubagentRun
type TaskRuntimeSample = contracts.TaskRuntimeSample
type TaskSemanticReport = contracts.TaskSemanticReport
type TaskEvent = contracts.TaskEvent
type TaskStatusView = contracts.TaskStatusView

type ChannelBinding = contracts.ChannelBinding
type ChannelConversation = contracts.ChannelConversation
type ChannelMessage = contracts.ChannelMessage
type ChannelTurnJob = contracts.ChannelTurnJob
type ChannelPendingAction = contracts.ChannelPendingAction
type EventBusLog = contracts.EventBusLog
type ChannelOutbox = contracts.ChannelOutbox
