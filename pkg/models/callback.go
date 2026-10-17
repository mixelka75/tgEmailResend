package models

// CallbackAction type of callback action
type CallbackAction string

const (
	CallbackMarkRead      CallbackAction = "mr"
	CallbackDelete        CallbackAction = "del" // asks for confirmation
	CallbackDeleteConfirm CallbackAction = "dy"
	CallbackDeleteCancel  CallbackAction = "dn"
	CallbackCopyCode      CallbackAction = "cc" // legacy buttons of old messages
)

// CallbackData structure for inline button callback
type CallbackData struct {
	Action    CallbackAction `json:"a"`
	MessageID int64          `json:"m"`
	CodeIndex int            `json:"c,omitempty"` // Code index for copying
}
