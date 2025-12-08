package formatter

import (
	"encoding/json"
	"fmt"

	"github.com/go-telegram/bot/models"

	appmodels "github.com/mixelka/emailresend/pkg/models"
)

// BuildEmailKeyboard creates an inline keyboard for an email message
func BuildEmailKeyboard(msgID int64, codes []appmodels.DetectedCode, isRead bool) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton

	// Code buttons (copy on click)
	if len(codes) > 0 {
		var codeButtons []models.InlineKeyboardButton
		for i, code := range codes {
			data := EncodeCallback(appmodels.CallbackData{
				Action:    appmodels.CallbackCopyCode,
				MessageID: msgID,
				CodeIndex: i,
			})
			codeButtons = append(codeButtons, models.InlineKeyboardButton{
				Text:         fmt.Sprintf("%s", code.Value),
				CallbackData: data,
			})
		}
		// Split into rows of 2 buttons each
		for i := 0; i < len(codeButtons); i += 2 {
			end := i + 2
			if end > len(codeButtons) {
				end = len(codeButtons)
			}
			rows = append(rows, codeButtons[i:end])
		}
	}

	// Action buttons
	actionRow := []models.InlineKeyboardButton{}

	if !isRead {
		actionRow = append(actionRow, models.InlineKeyboardButton{
			Text: "Прочитано",
			CallbackData: EncodeCallback(appmodels.CallbackData{
				Action:    appmodels.CallbackMarkRead,
				MessageID: msgID,
			}),
		})
	}

	actionRow = append(actionRow, models.InlineKeyboardButton{
		Text: "Удалить",
		CallbackData: EncodeCallback(appmodels.CallbackData{
			Action:    appmodels.CallbackDelete,
			MessageID: msgID,
		}),
	})

	rows = append(rows, actionRow)

	return &models.InlineKeyboardMarkup{
		InlineKeyboard: rows,
	}
}

// EncodeCallback encodes callback data to string
func EncodeCallback(data appmodels.CallbackData) string {
	b, _ := json.Marshal(data)
	return string(b)
}

// DecodeCallback decodes callback data from string
func DecodeCallback(data string) (appmodels.CallbackData, error) {
	var cb appmodels.CallbackData
	err := json.Unmarshal([]byte(data), &cb)
	return cb, err
}
