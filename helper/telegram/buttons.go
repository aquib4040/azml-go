package telegram

import (
	"encoding/json"
	"html"
	"strings"

	"github.com/gotd/td/tg"
)

type InlineButton struct {
	Text  string
	Data  string
	URL   string
	Style string // "primary" (blue), "success" (green), "danger" (red)
}

type ButtonMaker struct {
	buttons    []InlineButton
	rows       [][]InlineButton
	currentRow []InlineButton
}

func NewButtonMaker() *ButtonMaker {
	return &ButtonMaker{
		buttons:    make([]InlineButton, 0),
		rows:       make([][]InlineButton, 0),
		currentRow: make([]InlineButton, 0),
	}
}

func (b *ButtonMaker) IButton(text, data string) {
	btn := InlineButton{
		Text: text,
		Data: data,
	}
	b.buttons = append(b.buttons, btn)
	b.currentRow = append(b.currentRow, btn)
}

func (b *ButtonMaker) IButtonStyled(text, data, style string) {
	btn := InlineButton{
		Text:  text,
		Data:  data,
		Style: style,
	}
	b.buttons = append(b.buttons, btn)
	b.currentRow = append(b.currentRow, btn)
}

func (b *ButtonMaker) UButton(text, url string) {
	btn := InlineButton{
		Text: text,
		URL:  url,
	}
	b.buttons = append(b.buttons, btn)
	b.currentRow = append(b.currentRow, btn)
}

func (b *ButtonMaker) UButtonStyled(text, url, style string) {
	btn := InlineButton{
		Text:  text,
		URL:   url,
		Style: style,
	}
	b.buttons = append(b.buttons, btn)
	b.currentRow = append(b.currentRow, btn)
}

func (b *ButtonMaker) Row() {
	if len(b.currentRow) > 0 {
		b.rows = append(b.rows, b.currentRow)
		b.currentRow = nil
	}
}

func (b *ButtonMaker) Reset() {
	b.buttons = b.buttons[:0]
	b.rows = b.rows[:0]
	b.currentRow = b.currentRow[:0]
}

func (b *ButtonMaker) HasStyle() bool {
	for _, btn := range b.buttons {
		if btn.Style != "" {
			return true
		}
	}
	return false
}

func (b *ButtonMaker) getFinalRows(cols int) [][]InlineButton {
	if len(b.rows) > 0 || len(b.currentRow) > 0 {
		var res [][]InlineButton
		for _, r := range b.rows {
			if len(r) > 0 {
				res = append(res, r)
			}
		}
		if len(b.currentRow) > 0 {
			res = append(res, b.currentRow)
		}
		return res
	}

	if cols <= 0 {
		cols = 1
	}
	var res [][]InlineButton
	var curr []InlineButton
	for _, btn := range b.buttons {
		curr = append(curr, btn)
		if len(curr) >= cols {
			res = append(res, curr)
			curr = nil
		}
	}
	if len(curr) > 0 {
		res = append(res, curr)
	}
	return res
}

func (b *ButtonMaker) BuildBotAPIKeyboard(cols int) string {
	finalRows := b.getFinalRows(cols)
	if len(finalRows) == 0 {
		return ""
	}

	var rows [][]map[string]interface{}
	for _, row := range finalRows {
		var rowItems []map[string]interface{}
		for _, btn := range row {
			item := map[string]interface{}{
				"text": btn.Text,
			}
			if btn.URL != "" {
				item["url"] = btn.URL
			} else {
				item["callback_data"] = btn.Data
			}
			if btn.Style != "" {
				item["style"] = btn.Style
			}
			rowItems = append(rowItems, item)
		}
		if len(rowItems) > 0 {
			rows = append(rows, rowItems)
		}
	}

	payload := map[string]interface{}{
		"inline_keyboard": rows,
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

var smallCapsMap = map[rune]rune{
	'a': 'ᴀ', 'b': 'ʙ', 'c': 'ᴄ', 'd': 'ᴅ', 'e': 'ᴇ', 'f': 'ғ', 'g': 'ɢ', 'h': 'ʜ',
	'i': 'ɪ', 'j': 'ᴊ', 'k': 'ᴋ', 'l': 'ʟ', 'm': 'ᴍ', 'n': 'ɴ', 'o': 'ᴏ', 'p': 'ᴘ',
	'q': 'ǫ', 'r': 'ʀ', 's': 's', 't': 'ᴛ', 'u': 'ᴜ', 'v': 'ᴠ', 'w': 'ᴡ', 'x': 'x',
	'y': 'ʏ', 'z': 'ᴢ',
	'A': 'ᴀ', 'B': 'ʙ', 'C': 'ᴄ', 'D': 'ᴅ', 'E': 'ᴇ', 'F': 'ғ', 'G': 'ɢ', 'H': 'ʜ',
	'I': 'ɪ', 'J': 'ᴊ', 'K': 'ᴋ', 'L': 'ʟ', 'M': 'ᴍ', 'N': 'ɴ', 'O': 'ᴏ', 'P': 'ᴘ',
	'Q': 'ǫ', 'R': 'ʀ', 'S': 's', 'T': 'ᴛ', 'U': 'ᴜ', 'V': 'ᴠ', 'W': 'ᴡ', 'X': 'x',
	'Y': 'ʏ', 'Z': 'ᴢ',
}

func ToSmallCaps(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if sc, ok := smallCapsMap[r]; ok {
			sb.WriteRune(sc)
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func EscapeHTML(s string) string {
	return html.EscapeString(s)
}

func (b *ButtonMaker) BuildMenu(cols int) tg.ReplyMarkupClass {
	finalRows := b.getFinalRows(cols)
	if len(finalRows) == 0 {
		return nil
	}

	var rows []tg.KeyboardInlineButtonRow
	for _, row := range finalRows {
		var rowBtns []tg.KeyboardInlineButton
		for _, btn := range row {
			var btnType tg.InlineButtonTypeClass
			if btn.URL != "" {
				btnType = &tg.InlineButtonTypeURL{
					URL: btn.URL,
				}
			} else {
				btnType = &tg.InlineButtonTypeCallback{
					Data: []byte(btn.Data),
				}
			}

			rowBtns = append(rowBtns, tg.KeyboardInlineButton{
				Text: btn.Text,
				Type: btnType,
			})
		}
		if len(rowBtns) > 0 {
			rows = append(rows, tg.KeyboardInlineButtonRow{Buttons: rowBtns})
		}
	}

	return &tg.ReplyInlineMarkup{
		Rows: rows,
	}
}
