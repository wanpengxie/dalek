package contracts

type WorkerStatus string

const (
	WorkerCreating WorkerStatus = "creating"
	WorkerRunning  WorkerStatus = "running"
	WorkerStopped  WorkerStatus = "stopped"
	WorkerFailed   WorkerStatus = "failed"
)
