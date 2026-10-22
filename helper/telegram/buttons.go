package telegram

import "github.com/gotd/td/tg"

type InlineButton struct {
	Text string
	Data string
	URL  string
}

type ButtonMaker struct {
	buttons []InlineButton
}

func NewButtonMaker() *ButtonMaker {
	return &ButtonMaker{
		buttons: make([]InlineButton, 0),
	}
}

func (b *ButtonMaker) IButton(text, data string) {
	b.buttons = append(b.buttons, InlineButton{
		Text: text,
		Data: data,
	})
}

func (b *ButtonMaker) UButton(text, url string) {
	b.buttons = append(b.buttons, InlineButton{
		Text: text,
		URL:  url,
	})
}

func (b *ButtonMaker) Reset() {
	b.buttons = b.buttons[:0]
}

func (b *ButtonMaker) BuildMenu(cols int) tg.ReplyMarkupClass {
	if len(b.buttons) == 0 {
		return nil
	}
	if cols <= 0 {
		cols = 1
	}

	var rows []tg.KeyboardInlineButtonRow
	var currentRow []tg.KeyboardInlineButton

	for _, btn := range b.buttons {
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

		currentRow = append(currentRow, tg.KeyboardInlineButton{
			Text: btn.Text,
			Type: btnType,
		})
		if len(currentRow) >= cols {
			rows = append(rows, tg.KeyboardInlineButtonRow{Buttons: currentRow})
			currentRow = nil
		}
	}

	if len(currentRow) > 0 {
		rows = append(rows, tg.KeyboardInlineButtonRow{Buttons: currentRow})
	}

	return &tg.ReplyInlineMarkup{
		Rows: rows,
	}
}
