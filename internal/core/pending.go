package core

import "time"

// PendingAdd is a magnet link or a staged .torrent (see Stage) waiting to be shown in the "Add
// torrents" dialog, put there by the desktop app or a second launch when the user opens a magnet
// link or a .torrent file from outside the app (Explorer, "Open with"), instead of it being added
// silently. The web page picks these up and opens the dialog with them on the list.
type PendingAdd struct {
	Kind   string `json:"kind"` // "stage" (see Stage/StagedInfo) or "magnet"
	Stage  string `json:"stage,omitempty"`
	Magnet string `json:"magnet,omitempty"`
}

type queuedAdd struct {
	PendingAdd
	at time.Time
}

// PendingGrace is how long the window made for adding torrents has the queue to itself. The main window
// polls the queue too, and would take the items for its own dialog; it only gets what has waited this long,
// which is a sign that no window for adding came up (no WebView2, say).
const PendingGrace = 10 * time.Second

// QueueExternalAdd remembers one item for TakePendingAdds.
func (m *Manager) QueueExternalAdd(p PendingAdd) {
	m.mu.Lock()
	m.pending = append(m.pending, queuedAdd{PendingAdd: p, at: time.Now()})
	m.mu.Unlock()
}

// TakePendingAdds returns and clears the items queued by QueueExternalAdd. The window for adding takes all of
// them (forAddWindow); any other caller, the main window, only those that have waited PendingGrace.
func (m *Manager) TakePendingAdds(forAddWindow bool) []PendingAdd {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []PendingAdd
	var keep []queuedAdd
	for _, q := range m.pending {
		if forAddWindow || time.Since(q.at) >= PendingGrace {
			out = append(out, q.PendingAdd)
		} else {
			keep = append(keep, q)
		}
	}
	m.pending = keep
	return out
}
