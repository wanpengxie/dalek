package store

import "dalek/internal/contracts"

type ShapedItemStatus = contracts.ShapedItemStatus

const (
	ShapedPendingReview = contracts.ShapedPendingReview
	ShapedApproved      = contracts.ShapedApproved
	ShapedRejected      = contracts.ShapedRejected
	ShapedNeedsInfo     = contracts.ShapedNeedsInfo
)

type ShapedItem = contracts.ShapedItem
