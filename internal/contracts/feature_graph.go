package contracts

import "time"

const PMFeatureGraphSchemaV1 = "dalek.pm.feature_graph.v1"

type FeatureNodeType string

const (
	FeatureNodeRequirement FeatureNodeType = "requirement"
	FeatureNodeDesign      FeatureNodeType = "design"
	FeatureNodeTicket      FeatureNodeType = "ticket"
	FeatureNodeIntegration FeatureNodeType = "integration"
	FeatureNodeAcceptance  FeatureNodeType = "acceptance"
)

type FeatureNodeOwner string

const (
	FeatureNodeOwnerPM     FeatureNodeOwner = "pm"
	FeatureNodeOwnerWorker FeatureNodeOwner = "worker"
	FeatureNodeOwnerUser   FeatureNodeOwner = "user"
	FeatureNodeOwnerSystem FeatureNodeOwner = "system"
)

type FeatureNodeStatus string

const (
	FeatureNodePending    FeatureNodeStatus = "pending"
	FeatureNodeInProgress FeatureNodeStatus = "in_progress"
	FeatureNodeDone       FeatureNodeStatus = "done"
	FeatureNodeBlocked    FeatureNodeStatus = "blocked"
	FeatureNodeFailed     FeatureNodeStatus = "failed"
)

type FeatureNodeSize string

const (
	FeatureSizeXS FeatureNodeSize = "xs"
	FeatureSizeS  FeatureNodeSize = "s"
	FeatureSizeM  FeatureNodeSize = "m"
	FeatureSizeL  FeatureNodeSize = "l"
	FeatureSizeXL FeatureNodeSize = "xl"
)

type FeatureEdgeType string

const (
	FeatureEdgeDependsOn FeatureEdgeType = "depends_on"
	FeatureEdgeBlocks    FeatureEdgeType = "blocks"
	FeatureEdgeValidates FeatureEdgeType = "validates"
)

type FeatureDocRef struct {
	Kind string `json:"kind,omitempty"`
	Path string `json:"path"`
}

type FeatureNode struct {
	ID            string            `json:"id"`
	Type          FeatureNodeType   `json:"type"`
	Title         string            `json:"title"`
	Owner         FeatureNodeOwner  `json:"owner,omitempty"`
	Status        FeatureNodeStatus `json:"status"`
	DependsOn     []string          `json:"depends_on,omitempty"`
	DoneWhen      string            `json:"done_when,omitempty"`
	TouchSurfaces []string          `json:"touch_surfaces,omitempty"`
	EvidenceRefs  []string          `json:"evidence_refs,omitempty"`
	EstimatedSize FeatureNodeSize   `json:"estimated_size,omitempty"`
	TicketID      string            `json:"ticket_id,omitempty"`
	Notes         string            `json:"notes,omitempty"`
}

type FeatureEdge struct {
	From string          `json:"from"`
	To   string          `json:"to"`
	Type FeatureEdgeType `json:"type"`
}

type FeatureGraph struct {
	Schema       string          `json:"schema"`
	FeatureID    string          `json:"feature_id"`
	Goal         string          `json:"goal"`
	Docs         []FeatureDocRef `json:"docs,omitempty"`
	Nodes        []FeatureNode   `json:"nodes"`
	Edges        []FeatureEdge   `json:"edges,omitempty"`
	CurrentFocus string          `json:"current_focus,omitempty"`
	NextPMAction string          `json:"next_pm_action,omitempty"`
	UpdatedAt    time.Time       `json:"updated_at"`
}
