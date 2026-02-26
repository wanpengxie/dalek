package contracts

type PMDispatchJobStatus string

const (
	PMDispatchPending   PMDispatchJobStatus = "pending"
	PMDispatchRunning   PMDispatchJobStatus = "running"
	PMDispatchSucceeded PMDispatchJobStatus = "succeeded"
	PMDispatchFailed    PMDispatchJobStatus = "failed"
)
