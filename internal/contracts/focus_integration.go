package contracts

// CreateIntegrationTicketInput 描述 merge conflict 转 integration ticket 的最小证据集。
type CreateIntegrationTicketInput struct {
	SourceTicketIDs       []uint   `json:"source_ticket_ids"`
	TargetRef             string   `json:"target_ref"`
	ConflictTargetHeadSHA string   `json:"conflict_target_head_sha"`
	SourceAnchorSHAs      []string `json:"source_anchor_shas"`
	ConflictFiles         []string `json:"conflict_files"`
	MergeSummary          string   `json:"merge_summary"`
	EvidenceRefs          []string `json:"evidence_refs"`
}

type CreateIntegrationTicketResult struct {
	TicketID uint `json:"ticket_id"`
}
