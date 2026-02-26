package contracts

type MergeStatus string

const (
	MergeProposed      MergeStatus = "proposed"
	MergeChecksRunning MergeStatus = "checks_running"
	MergeReady         MergeStatus = "ready"
	MergeApproved      MergeStatus = "approved"
	MergeMerged        MergeStatus = "merged"
	MergeDiscarded     MergeStatus = "discarded"
	MergeBlocked       MergeStatus = "blocked"
)
