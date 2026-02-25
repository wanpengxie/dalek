package contracts

import (
	"fmt"
	"strings"
)

const PMDispatchJobResultSchemaV1 = "dalek.pm_dispatch_job_result.v1"

// PMDispatchJobResult 是 PM dispatch runner 的“最小审计输出”，会被写入 DB（ResultJSON）。
//
// 说明：
// - 该结构不用于 Go 解释策略，只用于 Go 侧最小结果记录与展示。
type PMDispatchJobResult struct {
	Schema      string `json:"schema"`
	InjectedCmd string `json:"injected_cmd"`

	// Worker loop 同步执行的结果字段（sdk 模式下有值）
	WorkerLoopStages     int    `json:"worker_loop_stages,omitempty"`
	WorkerLoopNextAction string `json:"worker_loop_next_action,omitempty"`
}

func (r PMDispatchJobResult) Validate() error {
	if strings.TrimSpace(r.Schema) != PMDispatchJobResultSchemaV1 {
		return fmt.Errorf("pm_dispatch_job_result schema 非法: %s", strings.TrimSpace(r.Schema))
	}
	return nil
}
