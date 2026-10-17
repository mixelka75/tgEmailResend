package formatter

import (
	"encoding/json"

	"github.com/go-telegram/bot/models"

	appmodels "github.com/mixelka/emailresend/pkg/models"
)

// Button styles (Bot API 9.4)
const (
	styleSuccess = "success"
	styleDanger  = "danger"
	stylePrimary = "primary"
)

// BuildEmailKeyboard creates an inline keyboard for an email message
func BuildEmailKeyboard(msgID int64, codes []appmodels.DetectedCode, isRead bool) *models.InlineKeyboardMarkup {
	rows := codeRows(codes)

	actionRow := []models.InlineKeyboardButton{}
	if !isRead {
		actionRow = append(actionRow, models.InlineKeyboardButton{
			Text:  "Прочитано",
			Style: styleSuccess,
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

	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// BuildDeleteConfirmKeyboard asks to confirm deleting an email
func BuildDeleteConfirmKeyboard(msgID int64, codes []appmodels.DetectedCode) *models.InlineKeyboardMarkup {
	rows := codeRows(codes)
	rows = append(rows, []models.InlineKeyboardButton{
		{
			Text:  "Да, удалить из ящика",
			Style: styleDanger,
			CallbackData: EncodeCallback(appmodels.CallbackData{
				Action:    appmodels.CallbackDeleteConfirm,
				MessageID: msgID,
			}),
		},
		{
			Text: "Отмена",
			CallbackData: EncodeCallback(appmodels.CallbackData{
				Action:    appmodels.CallbackDeleteCancel,
				MessageID: msgID,
			}),
		},
	})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// codeRows returns buttons that copy detected codes, two per row
func codeRows(codes []appmodels.DetectedCode) [][]models.InlineKeyboardButton {
	var rows [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton
	for _, code := range codes {
		row = append(row, models.InlineKeyboardButton{
			Text:     "Копировать " + code.Value,
			Style:    stylePrimary,
			CopyText: &models.CopyTextButton{Text: code.Value},
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
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
