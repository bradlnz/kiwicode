package main

func (e *editor) handleOperations(k key) {
	last := max(0, len(e.opsLines)-1)
	switch k.code {
	case keyUp:
		e.opsTop = max(0, e.opsTop-1)
	case keyDown:
		e.opsTop = min(last, e.opsTop+1)
	case keyPageUp:
		e.opsTop = max(0, e.opsTop-10)
	case keyPageDown:
		e.opsTop = min(last, e.opsTop+10)
	}
}
