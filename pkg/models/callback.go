package models

// CallbackAction type of callback action
type CallbackAction string

const (
	CallbackMarkRead CallbackAction = "mr"
	CallbackDelete   CallbackAction = "del"
	CallbackCopyCode CallbackAction = "cc"
)

// CallbackData structure for inline button callback
type CallbackData struct {
	Action    CallbackAction `json:"a"`
	MessageID int64          `json:"m"`
	CodeIndex int            `json:"c,omitempty"` // Code index for copying
}
