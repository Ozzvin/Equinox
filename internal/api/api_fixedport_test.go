package api

import (
	"bytes"
	"encoding/json"
	"testing"
)

// A port given at start (-torrent-port) is reported to the page and cannot be changed from it: a container
// publishes just that port. Saving the settings with the same port still works, as the page always sends it.
func TestFixedTorrentPort(t *testing.T) {
	e := setup(t, func(s *Server) { s.SetFixedPort(51420) })
	if err := e.m.SetListenPort(51420); err != nil { // what app.StartWith does before the engine starts
		t.Fatal(err)
	}
	base := `"downLimitKBps":0,"upLimitKBps":0,"altDownLimitKBps":0,"altUpLimitKBps":0,"ratioLimit":0,"maxActiveDownloads":0,"copyRemovePolicy":"with_data"`
	put := func(port string) int {
		r := e.do(t, "PUT", "/api/settings", bytes.NewReader([]byte(`{`+base+`,"listenPort":`+port+`}`)), nil)
		r.Body.Close()
		return r.StatusCode
	}
	if c := put("51757"); c != 400 {
		t.Fatalf("another port with a fixed one: %d, want 400", c)
	}
	if c := put("51420"); c != 200 {
		t.Fatalf("the same port: %d, want 200", c)
	}
	r := e.do(t, "GET", "/api/settings", nil, nil)
	var st struct {
		ListenPort int  `json:"listenPort"`
		PortFixed  bool `json:"portFixed"`
	}
	_ = json.NewDecoder(r.Body).Decode(&st)
	r.Body.Close()
	if st.ListenPort != 51420 || !st.PortFixed {
		t.Fatalf("settings: port %d fixed %v, want 51420 true", st.ListenPort, st.PortFixed)
	}

	free := setup(t) // without a fixed port nothing changes
	r = free.do(t, "GET", "/api/settings", nil, nil)
	st.PortFixed = false
	_ = json.NewDecoder(r.Body).Decode(&st)
	r.Body.Close()
	if st.PortFixed {
		t.Fatal("portFixed reported without a fixed port")
	}
}
