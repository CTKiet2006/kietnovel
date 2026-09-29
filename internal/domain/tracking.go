package domain

// StateChange is a record of a character/entity state change.
type StateChange struct {
	Chapter  int    `json:"chapter"`
	Entity   string `json:"entity"`              // the character name or the entity name
	Field    string `json:"field"`               // the changed attribute: realm/location/status/power/relation etc.
	OldValue string `json:"old_value,omitempty"` // before the change (may be empty on the first appearance)
	NewValue string `json:"new_value"`           // after the change
	Reason   string `json:"reason,omitempty"`    // the reason for the change
}
