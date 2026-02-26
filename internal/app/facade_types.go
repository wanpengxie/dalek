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

type TicketWorkflowStatus = contracts.TicketWorkflowStatus
type WorkerStatus = contracts.WorkerStatus

const (
	TicketBacklog  TicketWorkflowStatus = contracts.TicketBacklog
	TicketQueued   TicketWorkflowStatus = contracts.TicketQueued
	TicketActive   TicketWorkflowStatus = contracts.TicketActive
	TicketBlocked  TicketWorkflowStatus = contracts.TicketBlocked
	TicketDone     TicketWorkflowStatus = contracts.TicketDone
	TicketArchived TicketWorkflowStatus = contracts.TicketArchived
)

const (
	WorkerCreating WorkerStatus = contracts.WorkerCreating
	WorkerRunning  WorkerStatus = contracts.WorkerRunning
	WorkerStopped  WorkerStatus = contracts.WorkerStopped
	WorkerFailed   WorkerStatus = contracts.WorkerFailed
)

type TaskOwnerType = contracts.TaskOwnerType

const (
	TaskOwnerWorker   TaskOwnerType = contracts.TaskOwnerWorker
	TaskOwnerPM       TaskOwnerType = contracts.TaskOwnerPM
	TaskOwnerSubagent TaskOwnerType = contracts.TaskOwnerSubagent
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

type InboxStatus = contracts.InboxStatus

const (
	InboxOpen    InboxStatus = contracts.InboxOpen
	InboxDone    InboxStatus = contracts.InboxDone
	InboxSnoozed InboxStatus = contracts.InboxSnoozed
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

type MergeStatus = contracts.MergeStatus

const (
	MergeProposed      MergeStatus = contracts.MergeProposed
	MergeChecksRunning MergeStatus = contracts.MergeChecksRunning
	MergeReady         MergeStatus = contracts.MergeReady
	MergeApproved      MergeStatus = contracts.MergeApproved
	MergeMerged        MergeStatus = contracts.MergeMerged
	MergeDiscarded     MergeStatus = contracts.MergeDiscarded
	MergeBlocked       MergeStatus = contracts.MergeBlocked
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

type TaskRuntimeHealthState = contracts.TaskRuntimeHealthState

const (
	TaskHealthUnknown     TaskRuntimeHealthState = contracts.TaskHealthUnknown
	TaskHealthAlive       TaskRuntimeHealthState = contracts.TaskHealthAlive
	TaskHealthIdle        TaskRuntimeHealthState = contracts.TaskHealthIdle
	TaskHealthBusy        TaskRuntimeHealthState = contracts.TaskHealthBusy
	TaskHealthStalled     TaskRuntimeHealthState = contracts.TaskHealthStalled
	TaskHealthWaitingUser TaskRuntimeHealthState = contracts.TaskHealthWaitingUser
	TaskHealthDead        TaskRuntimeHealthState = contracts.TaskHealthDead
)

type TaskSemanticPhase = contracts.TaskSemanticPhase

const (
	TaskPhaseInit         TaskSemanticPhase = contracts.TaskPhaseInit
	TaskPhasePlanning     TaskSemanticPhase = contracts.TaskPhasePlanning
	TaskPhaseImplementing TaskSemanticPhase = contracts.TaskPhaseImplementing
	TaskPhaseTesting      TaskSemanticPhase = contracts.TaskPhaseTesting
	TaskPhaseReviewing    TaskSemanticPhase = contracts.TaskPhaseReviewing
	TaskPhaseDone         TaskSemanticPhase = contracts.TaskPhaseDone
	TaskPhaseBlocked      TaskSemanticPhase = contracts.TaskPhaseBlocked
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
