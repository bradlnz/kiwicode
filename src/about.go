package main

import (
	"kiwicode"
	"strings"
)

func (e *editor) openLicenses() {
	e.clearModalViews()
	e.explorer = false
	e.opsMode, e.opsTop = "Open Source Licenses", 0
	text := "KiwiCode\nhttps://github.com/bradlnz/kiwicode\n\n" + kiwicode.License + "\n" + kiwicode.ThirdPartyNotices
	e.opsLines = wrapTextLines(strings.Split(text, "\n"), max(1, e.codeAreaWidth()-2))
	e.status = "Licenses: arrows / Page Up / Page Down / wheel to scroll; Esc closes"
}
