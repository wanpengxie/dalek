package contracts

import (
	"fmt"
	"strings"
)

type ReportNextAction string

const (
	NextContinue ReportNextAction = "continue"
	NextWaitUser ReportNextAction = "wait_user"
	NextDone     ReportNextAction = "done"
)

const WorkerReportSchemaV1 = "dalek.report.v1"

type WorkerReport struct {
	Schema     string `json:"schema"`
	ReportedAt string `json:"reported_at"`

	ProjectKey string `json:"project_key"`
	WorkerID   uint   `json:"worker_id"`
	TicketID   uint   `json:"ticket_id"`
	TaskRunID  uint   `json:"task_run_id"`

	HeadSHA string `json:"head_sha"`
	Dirty   bool   `json:"dirty"`

	Summary    string   `json:"summary"`
	NeedsUser  bool     `json:"needs_user"`
	Blockers   []string `json:"blockers"`
	NextAction string   `json:"next_action"`
}

func (r *WorkerReport) Normalize() {
	if r == nil {
		return
	}
	if strings.TrimSpace(r.Schema) == "" {
		r.Schema = WorkerReportSchemaV1
	}
	r.NextAction = strings.TrimSpace(strings.ToLower(r.NextAction))
}

func (r WorkerReport) Validate() error {
	schema := strings.TrimSpace(r.Schema)
	if schema == "" {
		return fmt.Errorf("schema 为空")
	}
	if schema != WorkerReportSchemaV1 {
		return fmt.Errorf("schema 非法: %s", schema)
	}
	if r.WorkerID == 0 {
		return fmt.Errorf("worker_id 为空")
	}
	next := strings.TrimSpace(strings.ToLower(r.NextAction))
	switch next {
	case "", string(NextContinue), string(NextWaitUser), string(NextDone):
	default:
		return fmt.Errorf("next_action 非法: %s", strings.TrimSpace(r.NextAction))
	}
	return nil
}
