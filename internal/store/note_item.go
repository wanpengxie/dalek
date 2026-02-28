package store

import "dalek/internal/contracts"

type NoteStatus = contracts.NoteStatus

const (
	NoteOpen      = contracts.NoteOpen
	NoteShaping   = contracts.NoteShaping
	NoteShaped    = contracts.NoteShaped
	NoteDiscarded = contracts.NoteDiscarded
)

type NoteItem = contracts.NoteItem
