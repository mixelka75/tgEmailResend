package parser

import (
	"reflect"
	"testing"
)

func TestDetectCodes(t *testing.T) {
	d := NewCodeDetector()

	tests := []struct {
		name string
		text string
		want []string
	}{
		{"russian with words between", "Код подтверждения: 482913", []string{"482913"}},
		{"english sentence", "Your verification code is 1234.", []string{"1234"}},
		{"code on its own line", "Use this code to sign in:\n\n  739201  \n\nIt expires soon", []string{"739201"}},
		{"split code", "Your login code: 123 456", []string{"123456"}},
		{"short number before code", "Code expires in 10 minutes: 556677", []string{"556677"}},
		{"alphanumeric", "Recovery code: AB12CD", []string{"AB12CD"}},
		{"english word is not a code", "Your code below will expire", nil},
		{"year line is not a code", "Thanks\n2024\nBye", nil},
		{"keyboard is not a token", "keyboardshortcuts1", nil},
		{"no codes", "Привет! Как дела?", nil},
		{"long number is not a code", "Order 1234567890123 shipped", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, c := range d.DetectCodes(tt.text) {
				got = append(got, c.Value)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DetectCodes(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
