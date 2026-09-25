package model

type ControlLevel string

const (
	ControlLocked ControlLevel = "locked"
	ControlGuided ControlLevel = "guided"
	ControlOpen   ControlLevel = "open"
)
