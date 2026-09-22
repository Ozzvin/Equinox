package core

// PendingAdd is a magnet link or a staged .torrent (see Stage) waiting to be shown in the "Add
// torrents" dialog, put there by the desktop app or a second launch when the user opens a magnet
// link or a .torrent file from outside the app (Explorer, "Open with"), instead of it being added
// silently. The web page picks these up and opens the dialog with them on the list.
type PendingAdd struct {
	Kind   string `json:"kind"` // "stage" (see Stage/StagedInfo) or "magnet"
	Stage  string `json:"stage,omitempty"`
	Magnet string `json:"magnet,omitempty"`
}

// QueueExternalAdd remembers one item for TakePendingAdds.
func (m *Manager) QueueExternalAdd(p PendingAdd) {
	m.mu.Lock()
	m.pending = append(m.pending, p)
	m.mu.Unlock()
}

// TakePendingAdds returns and clears the items queued by QueueExternalAdd.
func (m *Manager) TakePendingAdds() []PendingAdd {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.pending
	m.pending = nil
	return out
}
