package main

func (e *editor) openArchitecture() {
	e.clearModalViews()
	e.opsMode, e.opsTop = "architecture canvas", 0
	e.explorer = false
	e.startNodeCanvas(true, false)
	e.status = "Architecture: Tab select · Enter open · arrows/drag pan · +/- zoom · [ ] links · F center · R refresh"
}
