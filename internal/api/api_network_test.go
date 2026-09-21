package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNetworkSettingsAndRestartRequired(t *testing.T) {
	e := setup(t)
	base := `"downLimitKBps":0,"upLimitKBps":0,"altDownLimitKBps":50,"altUpLimitKBps":50,"ratioLimit":0,"copyRemovePolicy":"with_data","maxActiveDownloads":0`
	put := func(extra string) int {
		r := e.do(t, "PUT", "/api/settings", bytes.NewReader([]byte(`{`+base+extra+`}`)), nil)
		r.Body.Close()
		return r.StatusCode
	}
	restart := func() (bool, []string) {
		r := e.do(t, "GET", "/api/restart-required", nil, nil)
		defer r.Body.Close()
		var ri struct {
			Required bool     `json:"required"`
			Reasons  []string `json:"reasons"`
		}
		_ = json.NewDecoder(r.Body).Decode(&ri)
		return ri.Required, ri.Reasons
	}
	net := func(mut string) string {
		return `,"network":{"maxConnsPerTorrent":40,"maxHalfOpenPerTorrent":25,"dht":true,"pex":true,"utp":true,"tcp":true,"ipv6":true,"webseeds":true,"acceptIncoming":true,"encryption":"prefer"` + mut + `}`
	}

	if req, _ := restart(); req {
		t.Fatal("fresh start must not ask for a restart")
	}
	if c := put(net("")); c != 200 { // only the live connection limit differs from the defaults
		t.Fatalf("live change: %d", c)
	}
	if req, _ := restart(); req {
		t.Fatal("changing the per-torrent limit must not ask for a restart")
	}
	if c := put(strings.Replace(net(""), `"dht":true`, `"dht":false`, 1)); c != 200 {
		t.Fatalf("dht off: %d", c)
	}
	if req, why := restart(); !req || len(why) != 1 || why[0] != "dht" {
		t.Fatalf("turning DHT off must ask for a restart: %v %v", req, why)
	}

	// The settings are stored and reported back.
	r := e.do(t, "GET", "/api/settings", nil, nil)
	var st struct {
		Network struct {
			Max int  `json:"maxConnsPerTorrent"`
			DHT bool `json:"dht"`
		} `json:"network"`
	}
	_ = json.NewDecoder(r.Body).Decode(&st)
	r.Body.Close()
	if st.Network.Max != 40 || st.Network.DHT {
		t.Fatalf("network settings not stored: %+v", st)
	}

	// Nonsense is refused and changes nothing; omitting "network" leaves it alone.
	for _, bad := range []string{`,"maxConnsPerTorrent":0`, `,"encryption":"maybe"`, `,"tcp":false,"utp":false`} {
		if c := put(net(bad)); c != 400 {
			t.Errorf("%q: status %d, want 400", bad, c)
		}
	}
	if c := put(""); c != 200 {
		t.Fatalf("put without network: %d", c)
	}
	r = e.do(t, "GET", "/api/settings", nil, nil)
	_ = json.NewDecoder(r.Body).Decode(&st)
	r.Body.Close()
	if st.Network.Max != 40 {
		t.Fatalf("a bad or missing network block changed the settings: %+v", st)
	}
}

func TestPerTorrentConnectionLimitEndpoint(t *testing.T) {
	e := setup(t)
	hash := upload(t, e, seededTorrent(t, e, bytes.Repeat([]byte{4}, 32<<10)))
	deadline := time.Now().Add(10 * time.Second)
	for len(e.m.List()) == 0 || !e.m.List()[0].HasMeta {
		if time.Now().After(deadline) {
			t.Fatal("torrent not ready")
		}
		time.Sleep(50 * time.Millisecond)
	}
	post := func(path, body string) int {
		r := e.do(t, "POST", path, bytes.NewReader([]byte(body)), nil)
		r.Body.Close()
		return r.StatusCode
	}
	if c := post("/api/torrents/"+hash+"/max-connections", `{"limit":12}`); c != 204 {
		t.Fatalf("set: %d", c)
	}
	r := e.do(t, "GET", "/api/torrents/"+hash+"/details", nil, nil)
	var d struct {
		MaxConns  int `json:"maxConns"`
		ConnLimit int `json:"connLimit"`
	}
	_ = json.NewDecoder(r.Body).Decode(&d)
	r.Body.Close()
	if d.MaxConns != 12 || d.ConnLimit != 12 {
		t.Fatalf("details: %+v", d)
	}
	for _, body := range []string{`{"limit":-1}`, `{"limit":5000}`, `nope`} {
		if c := post("/api/torrents/"+hash+"/max-connections", body); c != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, c)
		}
	}
	if c := post("/api/torrents/"+strings.Repeat("g", 40)+"/max-connections", `{"limit":5}`); c != 404 {
		t.Errorf("bad hash: %d, want 404", c)
	}
}
