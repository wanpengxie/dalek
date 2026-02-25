package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"dalek/internal/contracts"
	"dalek/internal/infra"
	"dalek/internal/repo"
	"dalek/internal/store"
)

// ProjectConfig 是 app 层对外暴露的项目配置类型。
type ProjectConfig = repo.Config

type ReportNextAction = contracts.ReportNextAction

const (
	NextContinue ReportNextAction = contracts.NextContinue
	NextWaitUser ReportNextAction = contracts.NextWaitUser
	NextDone     ReportNextAction = contracts.NextDone
)

type TailPreview = contracts.TailPreview
type WorkerReport = contracts.WorkerReport

const WorkerReportSchemaV1 = contracts.WorkerReportSchemaV1

type Ticket = store.Ticket
type Worker = store.Worker
type MergeItem = store.MergeItem
type InboxItem = store.InboxItem

type TicketWorkflowStatus = store.TicketWorkflowStatus
type WorkerStatus = store.WorkerStatus

const (
	TicketBacklog  TicketWorkflowStatus = store.TicketBacklog
	TicketQueued   TicketWorkflowStatus = store.TicketQueued
	TicketActive   TicketWorkflowStatus = store.TicketActive
	TicketBlocked  TicketWorkflowStatus = store.TicketBlocked
	TicketDone     TicketWorkflowStatus = store.TicketDone
	TicketArchived TicketWorkflowStatus = store.TicketArchived
)

const (
	WorkerCreating WorkerStatus = store.WorkerCreating
	WorkerRunning  WorkerStatus = store.WorkerRunning
	WorkerStopped  WorkerStatus = store.WorkerStopped
	WorkerFailed   WorkerStatus = store.WorkerFailed
)

type TaskOwnerType = store.TaskOwnerType

const (
	TaskOwnerWorker   TaskOwnerType = store.TaskOwnerWorker
	TaskOwnerPM       TaskOwnerType = store.TaskOwnerPM
	TaskOwnerSubagent TaskOwnerType = store.TaskOwnerSubagent
)

func ParseTaskOwnerType(raw string) (TaskOwnerType, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "", nil
	}
	switch TaskOwnerType(raw) {
	case TaskOwnerWorker, TaskOwnerPM, TaskOwnerSubagent:
		return TaskOwnerType(raw), nil
	default:
		return "", fmt.Errorf("owner 仅支持 worker|pm|subagent")
	}
}

type InboxStatus = store.InboxStatus

const (
	InboxOpen    InboxStatus = store.InboxOpen
	InboxDone    InboxStatus = store.InboxDone
	InboxSnoozed InboxStatus = store.InboxSnoozed
)

func ParseInboxStatus(raw string) (InboxStatus, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case string(InboxOpen):
		return InboxOpen, nil
	case string(InboxDone):
		return InboxDone, nil
	case string(InboxSnoozed):
		return InboxSnoozed, nil
	default:
		return "", fmt.Errorf("非法 inbox 状态: %s", strings.TrimSpace(raw))
	}
}

type MergeStatus = store.MergeStatus

const (
	MergeProposed      MergeStatus = store.MergeProposed
	MergeChecksRunning MergeStatus = store.MergeChecksRunning
	MergeReady         MergeStatus = store.MergeReady
	MergeApproved      MergeStatus = store.MergeApproved
	MergeMerged        MergeStatus = store.MergeMerged
	MergeDiscarded     MergeStatus = store.MergeDiscarded
	MergeBlocked       MergeStatus = store.MergeBlocked
)

func ParseMergeStatus(raw string) (MergeStatus, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "", nil
	}
	switch MergeStatus(raw) {
	case MergeProposed, MergeChecksRunning, MergeReady, MergeApproved, MergeMerged, MergeDiscarded, MergeBlocked:
		return MergeStatus(raw), nil
	default:
		return "", fmt.Errorf("非法 merge 状态: %s", strings.TrimSpace(raw))
	}
}

type TaskRuntimeHealthState = store.TaskRuntimeHealthState

const (
	TaskHealthUnknown     TaskRuntimeHealthState = store.TaskHealthUnknown
	TaskHealthAlive       TaskRuntimeHealthState = store.TaskHealthAlive
	TaskHealthIdle        TaskRuntimeHealthState = store.TaskHealthIdle
	TaskHealthBusy        TaskRuntimeHealthState = store.TaskHealthBusy
	TaskHealthStalled     TaskRuntimeHealthState = store.TaskHealthStalled
	TaskHealthWaitingUser TaskRuntimeHealthState = store.TaskHealthWaitingUser
	TaskHealthDead        TaskRuntimeHealthState = store.TaskHealthDead
)

type TaskSemanticPhase = store.TaskSemanticPhase

const (
	TaskPhaseInit         TaskSemanticPhase = store.TaskPhaseInit
	TaskPhasePlanning     TaskSemanticPhase = store.TaskPhasePlanning
	TaskPhaseImplementing TaskSemanticPhase = store.TaskPhaseImplementing
	TaskPhaseTesting      TaskSemanticPhase = store.TaskPhaseTesting
	TaskPhaseReviewing    TaskSemanticPhase = store.TaskPhaseReviewing
	TaskPhaseDone         TaskSemanticPhase = store.TaskPhaseDone
	TaskPhaseBlocked      TaskSemanticPhase = store.TaskPhaseBlocked
)

func TmuxSocketDir(tmpDir string, uid int) string {
	tmpDir = strings.TrimSpace(tmpDir)
	if tmpDir == "" {
		tmpDir = strings.TrimSpace(os.Getenv("TMUX_TMPDIR"))
	}
	if tmpDir == "" {
		tmpDir = "/tmp"
	}
	if uid <= 0 {
		uid = os.Getuid()
	}
	return filepath.Join(tmpDir, "tmux-"+strconv.Itoa(uid))
}

func ListTmuxSocketFiles(tmpDir string) ([]string, error) {
	dir := TmuxSocketDir(tmpDir, 0)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSpace(e.Name())
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func ListTmuxSessions(ctx context.Context, socket string) (map[string]bool, error) {
	return infra.NewTmuxExecClient().ListSessions(ctx, strings.TrimSpace(socket))
}

func KillTmuxServer(ctx context.Context, socket string) error {
	return infra.NewTmuxExecClient().KillServer(ctx, strings.TrimSpace(socket))
}

func KillTmuxSession(ctx context.Context, socket, session string) error {
	return infra.NewTmuxExecClient().KillSession(ctx, strings.TrimSpace(socket), strings.TrimSpace(session))
}
