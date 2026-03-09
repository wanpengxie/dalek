package contracts

import "strings"

type IntegrationStatus string

const (
	IntegrationNone       IntegrationStatus = ""
	IntegrationNeedsMerge IntegrationStatus = "needs_merge"
	IntegrationMerged     IntegrationStatus = "merged"
	IntegrationAbandoned  IntegrationStatus = "abandoned"
)

func CanonicalIntegrationStatus(st IntegrationStatus) IntegrationStatus {
	v := IntegrationStatus(strings.TrimSpace(strings.ToLower(string(st))))
	switch v {
	case "", "none", "pending":
		return IntegrationNone
	case "needs_merge", "needs-merge", "needsmerge", "pending_merge", "pending-merge":
		return IntegrationNeedsMerge
	case "merged", "integrated", "done":
		return IntegrationMerged
	case "abandoned", "discarded", "dropped":
		return IntegrationAbandoned
	default:
		return v
	}
}
