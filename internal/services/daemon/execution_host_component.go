package daemon

import "context"

func (h *ExecutionHost) Name() string {
	return "execution_host"
}

func (h *ExecutionHost) Start(ctx context.Context) error {
	return nil
}
