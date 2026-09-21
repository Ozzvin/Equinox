package core

// Event is something the user may want to hear about even when no window is open. The desktop
// application turns these into Windows notifications.
type Event struct {
	Kind   string // "completed" (a download finished) or "limit" (seeding stopped by a limit)
	Hash   string
	Name   string
	Detail string // for "limit": what was reached
}

// OnEvent sets the function that receives events (nil removes it). It is called on its own
// goroutine, so it may take its time.
func (m *Manager) OnEvent(fn func(Event)) {
	m.evMu.Lock()
	m.onEvent = fn
	m.evMu.Unlock()
}

func (m *Manager) emit(e Event) {
	m.evMu.Lock()
	fn := m.onEvent
	m.evMu.Unlock()
	if fn != nil {
		go fn(e)
	}
}
