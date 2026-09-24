package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/config"
	"github.com/Ozzvin/equinox/internal/core"
)

const tok = "0123456789abcdef0123456789abcdef0123"

type env struct {
	dir string
	m   *core.Manager
	srv *httptest.Server
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Load(filepath.Join(dir, "settings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = cfg.Update(func(s *config.Settings) {
		s.ListenPort, s.PortMapping = 0, false
		s.TorrentCopyDir = filepath.Join(dir, "torrents") // copies are off by default; the tests here check them
	})
	m, err := core.New(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(m, cfg, tok, nil))
	t.Cleanup(func() { srv.Close(); m.Close(); time.Sleep(500 * time.Millisecond) })
	return &env{dir, m, srv}
}

func (e *env) do(t *testing.T, method, path string, body io.Reader, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	req.Header.Set("Authorization", "Bearer "+tok)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// seededTorrent puts a finished file into the download dir and returns its .torrent bytes,
// so the engine verifies it and reports the torrent as complete.
func seededTorrent(t *testing.T, e *env, payload []byte) []byte {
	t.Helper()
	dl := filepath.Join(e.dir, "downloads")
	_ = os.MkdirAll(dl, 0o755)
	p := filepath.Join(dl, "clip.mkv")
	if err := os.WriteFile(p, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(p); err != nil {
		t.Fatal(err)
	}
	b, _ := bencode.Marshal(info)
	var buf bytes.Buffer
	if err := (&metainfo.MetaInfo{InfoBytes: b}).Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func upload(t *testing.T, e *env, torrent []byte) string {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "clip.torrent")
	fw.Write(torrent)
	mw.Close()
	res := e.do(t, "POST", "/api/torrents", &body, map[string]string{"Content-Type": mw.FormDataContentType()})
	defer res.Body.Close()
	if res.StatusCode != 201 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("add: %d %s", res.StatusCode, b)
	}
	var out struct{ Hash string }
	_ = json.NewDecoder(res.Body).Decode(&out)
	return out.Hash
}

func TestAuthRequired(t *testing.T) {
	e := setup(t)
	res, _ := http.Get(e.srv.URL + "/api/torrents")
	if res.StatusCode != 401 {
		t.Fatalf("expected 401 without token, got %d", res.StatusCode)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/torrents", nil)
	req.Host = "evil.example.com"
	req.Header.Set("Authorization", "Bearer "+tok)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 403 {
		t.Fatalf("expected 403 for foreign Host header, got %d", res.StatusCode)
	}
}

func TestStreamRangeAndRemoveWithData(t *testing.T) {
	e := setup(t)
	payload := make([]byte, 200<<10)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	hash := upload(t, e, seededTorrent(t, e, payload))

	// Wait until the engine has verified the existing data.
	deadline := time.Now().Add(10 * time.Second)
	for {
		l := e.m.List()
		if len(l) == 1 && l[0].Progress == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("torrent never completed: %+v", l)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Range request, as a media player seeking would send it.
	res := e.do(t, "GET", "/api/torrents/"+hash+"/files/0/stream", nil, map[string]string{"Range": "bytes=1000-1999"})
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusPartialContent || !bytes.Equal(b, payload[1000:2000]) {
		t.Fatalf("range: status %d, %d bytes, match=%v", res.StatusCode, len(b), bytes.Equal(b, payload[1000:2000]))
	}

	// Token in the query works for players on the stream URL only.
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/torrents/"+hash+"/files/0/stream?token="+tok, nil)
	r2, _ := http.DefaultClient.Do(req)
	_, _ = io.Copy(io.Discard, r2.Body)
	r2.Body.Close()
	if r2.StatusCode != 200 {
		t.Fatalf("stream with ?token: %d", r2.StatusCode)
	}
	req, _ = http.NewRequest("GET", e.srv.URL+"/api/torrents?token="+tok, nil)
	if r3, _ := http.DefaultClient.Do(req); r3.StatusCode != 401 {
		t.Fatalf("?token must not work outside /stream, got %d", r3.StatusCode)
	}

	// Remove with data: file and .torrent copy are gone.
	copyPath := filepath.Join(e.dir, "torrents", "clip.mkv.torrent")
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatal("copy missing before removal")
	}
	res = e.do(t, "DELETE", "/api/torrents/"+hash+"?data=1", nil, nil)
	if res.StatusCode != 204 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("delete: %d %s", res.StatusCode, b)
	}
	for _, p := range []string{copyPath, filepath.Join(e.dir, "downloads", "clip.mkv")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s still exists", p)
		}
	}
}

func TestTurtleToggle(t *testing.T) {
	e := setup(t)
	res := e.do(t, "POST", "/api/altspeed", bytes.NewReader([]byte(`{"enabled":true}`)), nil)
	if res.StatusCode != 200 {
		t.Fatalf("altspeed: %d", res.StatusCode)
	}
	res = e.do(t, "GET", "/api/stats", nil, nil)
	var st struct{ AltSpeed bool }
	_ = json.NewDecoder(res.Body).Decode(&st)
	if !st.AltSpeed {
		t.Fatal("turtle mode not active")
	}
}

func TestPortReportEndpoint(t *testing.T) {
	e := setup(t) // port mapping is disabled in the test setup
	res := e.do(t, "GET", "/api/port", nil, nil)
	var p struct {
		Verdict, Advice string
		Enabled         bool
		Port            int
	}
	_ = json.NewDecoder(res.Body).Decode(&p)
	if p.Verdict != "manual" || p.Advice == "" || p.Enabled || p.Port == 0 {
		t.Fatalf("unexpected report: %+v", p)
	}
	// Switching off when already off must be a harmless no-op, and must not touch the router.
	res = e.do(t, "POST", "/api/port/mapping", bytes.NewReader([]byte(`{"enabled":false}`)), nil)
	if res.StatusCode != 200 {
		t.Fatalf("mapping toggle: %d", res.StatusCode)
	}
}

func TestSettingsScheduleAndQueueRoundTrip(t *testing.T) {
	e := setup(t)
	put := func(body string) *http.Response {
		return e.do(t, "PUT", "/api/settings", bytes.NewReader([]byte(body)), nil)
	}
	base := `"downLimitKBps":0,"upLimitKBps":0,"altDownLimitKBps":50,"altUpLimitKBps":50,"ratioLimit":0,"copyRemovePolicy":"with_data"`

	res := put(`{` + base + `,"maxActiveDownloads":3,"altSchedule":{"enabled":true,"from":"22:30","to":"06:15","days":[1,2,3]}}`)
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("put: %d %s", res.StatusCode, b)
	}
	var got struct {
		MaxActiveDownloads int `json:"maxActiveDownloads"`
		AltSchedule        struct {
			Enabled  bool
			From, To string
			Days     []int
		} `json:"altSchedule"`
	}
	get := func() {
		r := e.do(t, "GET", "/api/settings", nil, nil)
		_ = json.NewDecoder(r.Body).Decode(&got)
	}
	get()
	if got.MaxActiveDownloads != 3 || !got.AltSchedule.Enabled || got.AltSchedule.From != "22:30" || len(got.AltSchedule.Days) != 3 {
		t.Fatalf("settings not stored: %+v", got)
	}

	// A client that does not know about the schedule leaves it alone.
	if res := put(`{` + base + `,"maxActiveDownloads":3}`); res.StatusCode != 200 {
		t.Fatalf("put without schedule: %d", res.StatusCode)
	}
	get()
	if !got.AltSchedule.Enabled || got.AltSchedule.To != "06:15" {
		t.Fatalf("omitted schedule must stay unchanged: %+v", got)
	}

	if res := put(`{` + base + `,"maxActiveDownloads":0,"altSchedule":{"enabled":true,"from":"99:99","to":"06:00"}}`); res.StatusCode != 400 {
		t.Fatalf("bad schedule must be rejected, got %d", res.StatusCode)
	}
	if res := put(`{` + base + `,"maxActiveDownloads":-1}`); res.StatusCode != 400 {
		t.Fatalf("negative limit must be rejected, got %d", res.StatusCode)
	}
	// So is the period of the update check: stored when given, kept when omitted, refused outside 5 min .. 14 days.
	var upd struct {
		Minutes int `json:"updateCheckMinutes"`
	}
	getUpd := func() {
		r := e.do(t, "GET", "/api/settings", nil, nil)
		_ = json.NewDecoder(r.Body).Decode(&upd)
	}
	getUpd()
	if upd.Minutes != 60 {
		t.Fatalf("a new install checks every hour, got %d", upd.Minutes)
	}
	if res := put(`{` + base + `,"maxActiveDownloads":3,"updateCheckMinutes":15}`); res.StatusCode != 200 {
		t.Fatalf("put update period: %d", res.StatusCode)
	}
	getUpd()
	if upd.Minutes != 15 {
		t.Fatalf("update period not stored: %d", upd.Minutes)
	}
	if res := put(`{` + base + `,"maxActiveDownloads":3}`); res.StatusCode != 200 {
		t.Fatalf("put without update period: %d", res.StatusCode)
	}
	getUpd()
	if upd.Minutes != 15 {
		t.Fatalf("omitted update period must stay unchanged: %d", upd.Minutes)
	}
	for _, bad := range []string{"0", "4", "-1", "20161"} {
		if res := put(`{` + base + `,"maxActiveDownloads":3,"updateCheckMinutes":` + bad + `}`); res.StatusCode != 400 {
			t.Fatalf("update period %s must be rejected, got %d", bad, res.StatusCode)
		}
	}

	// The seed limit is optional too: stored when given, left alone when omitted, refused when negative.
	if res := put(`{` + base + `,"maxActiveDownloads":3,"maxActiveSeeds":4}`); res.StatusCode != 200 {
		t.Fatalf("put seed limit: %d", res.StatusCode)
	}
	var seeds struct {
		MaxActiveSeeds int `json:"maxActiveSeeds"`
	}
	getSeeds := func() {
		r := e.do(t, "GET", "/api/settings", nil, nil)
		_ = json.NewDecoder(r.Body).Decode(&seeds)
	}
	getSeeds()
	if seeds.MaxActiveSeeds != 4 {
		t.Fatalf("seed limit not stored: %+v", seeds)
	}
	if res := put(`{` + base + `,"maxActiveDownloads":3}`); res.StatusCode != 200 {
		t.Fatalf("put without seed limit: %d", res.StatusCode)
	}
	getSeeds()
	if seeds.MaxActiveSeeds != 4 {
		t.Fatalf("omitted seed limit must stay unchanged: %+v", seeds)
	}
	if res := put(`{` + base + `,"maxActiveDownloads":3,"maxActiveSeeds":-1}`); res.StatusCode != 400 {
		t.Fatalf("negative seed limit must be rejected, got %d", res.StatusCode)
	}
}

// Info hashes come straight from the URL; garbage must give a clean 404, never a panic.
func TestBadHashesAreRejectedCleanly(t *testing.T) {
	e := setup(t)
	for _, hash := range []string{"abc", strings.Repeat("z", 40), strings.Repeat("a", 81), "%00", strings.Repeat("a", 39)} {
		for _, call := range [][2]string{
			{"POST", "/api/torrents/" + hash + "/pause"},
			{"POST", "/api/torrents/" + hash + "/resume"},
			{"GET", "/api/torrents/" + hash + "/files"},
			{"GET", "/api/torrents/" + hash + "/files/0/stream"},
			{"DELETE", "/api/torrents/" + hash + "?data=1"},
		} {
			res := e.do(t, call[0], call[1], nil, nil)
			res.Body.Close()
			if res.StatusCode != 404 {
				t.Errorf("%s %s: status %d, want 404", call[0], call[1], res.StatusCode)
			}
		}
	}
	for _, path := range []string{"/label", "/queue", "/sequential", "/files/priority"} {
		res := e.do(t, "POST", "/api/torrents/"+strings.Repeat("g", 40)+path, bytes.NewReader([]byte(`{"label":"x","move":"up","enabled":true,"files":[0],"priority":"skip"}`)), nil)
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("POST %s with bad hash: status %d, want 404", path, res.StatusCode)
		}
	}
}

func TestMoveEndpointAndCompletedFolderSetting(t *testing.T) {
	e := setup(t)
	payload := bytes.Repeat([]byte{5}, 64<<10)
	hash := upload(t, e, seededTorrent(t, e, payload))
	deadline := time.Now().Add(10 * time.Second)
	for {
		l := e.m.List()
		if len(l) == 1 && l[0].Progress == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("torrent never completed")
		}
		time.Sleep(50 * time.Millisecond)
	}

	target := filepath.Join(e.dir, "library")
	res := e.do(t, "POST", "/api/torrents/"+hash+"/move", bytes.NewReader([]byte(`{"path":`+strconvQuote(target)+`}`)), nil)
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("move: %d", res.StatusCode)
	}
	for {
		l := e.m.List()
		if len(l) == 1 && l[0].SavePath == target && l[0].Moving == 0 {
			break
		}
		if time.Now().After(deadline.Add(10 * time.Second)) {
			t.Fatalf("move never finished: %+v", l)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(target, "clip.mkv")); err != nil {
		t.Fatal("file missing in the new folder")
	}

	for _, body := range []string{`{}`, `{"path":""}`, `not json`} {
		r := e.do(t, "POST", "/api/torrents/"+hash+"/move", bytes.NewReader([]byte(body)), nil)
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Errorf("body %q: status %d, want 400", body, r.StatusCode)
		}
	}

	// The completed-downloads folder is a normal setting: created, validated, stored.
	done := filepath.Join(e.dir, "finished")
	base := `"downLimitKBps":0,"upLimitKBps":0,"altDownLimitKBps":50,"altUpLimitKBps":50,"ratioLimit":0,"copyRemovePolicy":"with_data","maxActiveDownloads":0`
	r := e.do(t, "PUT", "/api/settings", bytes.NewReader([]byte(`{`+base+`,"moveCompletedDir":`+strconvQuote(done)+`}`)), nil)
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("put moveCompletedDir: %d", r.StatusCode)
	}
	if _, err := os.Stat(done); err != nil {
		t.Fatal("completed folder was not created")
	}
	r = e.do(t, "GET", "/api/settings", nil, nil)
	var st struct {
		MoveCompletedDir string `json:"moveCompletedDir"`
	}
	_ = json.NewDecoder(r.Body).Decode(&st)
	if st.MoveCompletedDir != done {
		t.Fatalf("not stored: %q", st.MoveCompletedDir)
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestDetailsPeersTrackersRecheckEndpoints(t *testing.T) {
	e := setup(t)
	payload := bytes.Repeat([]byte{3}, 64<<10)
	hash := upload(t, e, seededTorrent(t, e, payload))
	deadline := time.Now().Add(10 * time.Second)
	for {
		if l := e.m.List(); len(l) == 1 && l[0].Progress == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("torrent never completed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	base := "/api/torrents/" + hash

	res := e.do(t, "GET", base+"/details", nil, nil)
	var d struct {
		Name      string `json:"name"`
		Pieces    int    `json:"pieces"`
		Magnet    string `json:"magnet"`
		TotalSize int64  `json:"totalSize"`
	}
	_ = json.NewDecoder(res.Body).Decode(&d)
	res.Body.Close()
	if res.StatusCode != 200 || d.Name != "clip.mkv" || d.Pieces != 4 || d.TotalSize != 64<<10 || !strings.HasPrefix(d.Magnet, "magnet:?xt=urn:btih:") {
		t.Fatalf("details: %d %+v", res.StatusCode, d)
	}

	res = e.do(t, "GET", base+"/peers", nil, nil)
	var peers []map[string]any
	_ = json.NewDecoder(res.Body).Decode(&peers)
	res.Body.Close()
	if res.StatusCode != 200 || peers == nil {
		t.Fatalf("peers must be a JSON array even when empty: %d %v", res.StatusCode, peers)
	}

	add := func(u string) int {
		r := e.do(t, "POST", base+"/trackers", bytes.NewReader([]byte(`{"url":`+strconvQuote(u)+`}`)), nil)
		r.Body.Close()
		return r.StatusCode
	}
	if c := add("udp://tracker.example.org:6969/announce"); c != 204 {
		t.Fatalf("add tracker: %d", c)
	}
	if c := add("javascript:alert(1)"); c != 400 {
		t.Fatalf("bad tracker must be 400, got %d", c)
	}

	// Peers added by hand: the answer says how many were new and which addresses could not be used.
	addPeers := func(body string) (int, core.PeerAddResult) {
		r := e.do(t, "POST", base+"/peers", bytes.NewReader([]byte(body)), nil)
		defer r.Body.Close()
		var out core.PeerAddResult
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	code, pr := addPeers(`{"peers":["203.0.113.5:6881","nonsense","203.0.113.5:0"]}`)
	if code != 200 || pr.Added+pr.Known != 1 || len(pr.Errors) != 2 || pr.Errors[0].Reason != "format" || pr.Errors[1].Reason != "port" {
		t.Fatalf("add peers: %d %+v", code, pr)
	}
	if code, _ := addPeers(`{"peers":[]}`); code != 400 {
		t.Fatalf("an empty list of peers must be 400, got %d", code)
	}
	if code, _ := addPeers(`not json`); code != 400 {
		t.Fatalf("a bad body must be 400, got %d", code)
	}

	r := e.do(t, "POST", base+"/recheck", nil, nil)
	r.Body.Close()
	if r.StatusCode != http.StatusAccepted {
		t.Fatalf("recheck: %d", r.StatusCode)
	}
	bad := "/api/torrents/" + strings.Repeat("z", 40)
	for _, c := range [][2]string{{"GET", bad + "/details"}, {"GET", bad + "/peers"}, {"POST", bad + "/trackers"}, {"POST", bad + "/peers"}, {"POST", bad + "/recheck"}} {
		r := e.do(t, c[0], c[1], bytes.NewReader([]byte(`{"url":"udp://t.example:1/a","peers":["203.0.113.5:6881"]}`)), nil)
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Errorf("%s %s: %d, want 404", c[0], c[1], r.StatusCode)
		}
	}
}

// The first-run guide marks itself done through the settings; other saves leave the mark alone.
func TestSetupDoneIsStoredAndKept(t *testing.T) {
	e := setup(t)
	put := func(body string) {
		r := e.do(t, "PUT", "/api/settings", bytes.NewReader([]byte(body)), nil)
		if r.StatusCode != 200 {
			t.Fatalf("put %s: %d", body, r.StatusCode)
		}
	}
	done := func() bool {
		var s struct {
			SetupDone bool `json:"setupDone"`
		}
		r := e.do(t, "GET", "/api/settings", nil, nil)
		_ = json.NewDecoder(r.Body).Decode(&s)
		return s.SetupDone
	}
	base := `"downLimitKBps":0,"upLimitKBps":0,"altDownLimitKBps":0,"altUpLimitKBps":0,"ratioLimit":0,"maxActiveDownloads":0,"copyRemovePolicy":"with_data"`
	if done() {
		t.Fatal("a new installation has not been through the guide")
	}
	put(`{` + base + `,"setupDone":true}`)
	if !done() {
		t.Fatal("setupDone was not stored")
	}
	put(`{` + base + `}`) // the ordinary settings form does not know the flag
	if !done() {
		t.Fatal("a save without the flag must keep it")
	}
}

// The window setting is off by default and is switched through the ordinary settings save.
func TestRememberWindowSetting(t *testing.T) {
	e := setup(t)
	get := func() bool {
		var s struct {
			RememberWindow bool `json:"rememberWindow"`
		}
		r := e.do(t, "GET", "/api/settings", nil, nil)
		_ = json.NewDecoder(r.Body).Decode(&s)
		return s.RememberWindow
	}
	put := func(extra string) {
		body := `{"downLimitKBps":0,"upLimitKBps":0,"altDownLimitKBps":0,"altUpLimitKBps":0,"ratioLimit":0,"maxActiveDownloads":0,"copyRemovePolicy":"with_data"` + extra + `}`
		if r := e.do(t, "PUT", "/api/settings", bytes.NewReader([]byte(body)), nil); r.StatusCode != 200 {
			t.Fatalf("put: %d", r.StatusCode)
		}
	}
	if get() {
		t.Fatal("remembering the window must be off by default")
	}
	put(`,"rememberWindow":true`)
	if !get() {
		t.Fatal("not stored")
	}
	put(``)
	if !get() {
		t.Fatal("a save without the field must keep it")
	}
	put(`,"rememberWindow":false`)
	if get() {
		t.Fatal("not switched off")
	}
}
