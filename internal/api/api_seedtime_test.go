package api

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSeedTimeAndNotifySettings(t *testing.T) {
	e := setup(t)
	put := func(body string) (int, map[string]any) {
		r := e.do(t, "PUT", "/api/settings", strings.NewReader(body), nil)
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	base := `"downLimitKBps":0,"upLimitKBps":0,"altDownLimitKBps":0,"altUpLimitKBps":0,"ratioLimit":0,"maxActiveDownloads":0,"copyRemovePolicy":"with_data"`
	code, out := put(`{` + base + `,"seedTimeLimitMinutes":120,"notifyOnComplete":false}`)
	if code != 200 || out["seedTimeLimitMinutes"] != float64(120) || out["notifyOnComplete"] != false {
		t.Fatalf("not saved: %d %v", code, out)
	}
	// Omitted fields stay as they were.
	if code, out = put(`{` + base + `}`); code != 200 || out["seedTimeLimitMinutes"] != float64(120) || out["notifyOnComplete"] != false {
		t.Fatalf("omitted must be unchanged: %d %v", code, out)
	}
	if code, _ = put(`{` + base + `,"seedTimeLimitMinutes":-5}`); code != 400 {
		t.Fatalf("negative must give 400, got %d", code)
	}

	for name, tc := range map[string]struct {
		path, body string
		want       int
	}{
		"unknown torrent": {"/api/torrents/" + strings.Repeat("a", 40) + "/seed-time-limit", `{"minutes":10}`, 404},
		"garbage":         {"/api/torrents/" + strings.Repeat("a", 40) + "/seed-time-limit", `nope`, 400},
	} {
		r := e.do(t, "POST", tc.path, bytes.NewReader([]byte(tc.body)), nil)
		r.Body.Close()
		if r.StatusCode != tc.want {
			t.Errorf("%s: %d, want %d", name, r.StatusCode, tc.want)
		}
	}
}

func TestLabelColorsSetting(t *testing.T) {
	e := setup(t)
	put := func(extra string) (int, map[string]any) {
		body := `{"downLimitKBps":0,"upLimitKBps":0,"altDownLimitKBps":0,"altUpLimitKBps":0,"ratioLimit":0,"maxActiveDownloads":0,"copyRemovePolicy":"with_data"` + extra + `}`
		r := e.do(t, "PUT", "/api/settings", strings.NewReader(body), nil)
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	code, out := put(`,"labelColors":{"Фильмы":"violet","  ":"pink","Игры":""}`)
	colors, _ := out["labelColors"].(map[string]any)
	if code != 200 || len(colors) != 1 || colors["Фильмы"] != "violet" {
		t.Fatalf("colours: %d %v (blank names and empty colours are dropped)", code, out["labelColors"])
	}
	if code, out = put(``); code != 200 || len(out["labelColors"].(map[string]any)) != 1 {
		t.Fatalf("omitted must leave them as they were: %d %v", code, out["labelColors"])
	}
	if code, _ = put(`,"labelColors":{"Фильмы":"green"}`); code != 400 {
		t.Fatalf("a colour outside the palette must be refused, got %d", code)
	}
}

func TestChecksLimitAndAddPausedSettings(t *testing.T) {
	e := setup(t)
	put := func(extra string) (int, map[string]any) {
		body := `{"downLimitKBps":0,"upLimitKBps":0,"altDownLimitKBps":0,"altUpLimitKBps":0,"ratioLimit":0,"maxActiveDownloads":0,"copyRemovePolicy":"with_data"` + extra + `}`
		r := e.do(t, "PUT", "/api/settings", strings.NewReader(body), nil)
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	code, out := put(`,"maxConcurrentChecks":3,"addPaused":true`)
	add, _ := out["add"].(map[string]any)
	if code != 200 || out["maxConcurrentChecks"] != float64(3) || add["paused"] != true {
		t.Fatalf("not saved: %d %v", code, out)
	}
	if code, out = put(``); code != 200 || out["maxConcurrentChecks"] != float64(3) || out["add"].(map[string]any)["paused"] != true {
		t.Fatalf("omitted must stay: %d %v", code, out)
	}
	for _, bad := range []string{`,"maxConcurrentChecks":-1`, `,"maxConcurrentChecks":65`} {
		if code, _ := put(bad); code != 400 {
			t.Errorf("%s must give 400, got %d", bad, code)
		}
	}
	if code, out = put(`,"maxConcurrentChecks":0,"addPaused":false`); code != 200 || out["maxConcurrentChecks"] != float64(0) || out["add"].(map[string]any)["paused"] != false {
		t.Fatalf("zero and false must be storable: %d %v", code, out)
	}
}

func TestSpeedUnitSetting(t *testing.T) {
	e := setup(t)
	put := func(extra string) (int, map[string]any) {
		body := `{"downLimitKBps":0,"upLimitKBps":0,"altDownLimitKBps":0,"altUpLimitKBps":0,"ratioLimit":0,"maxActiveDownloads":0,"copyRemovePolicy":"with_data"` + extra + `}`
		r := e.do(t, "PUT", "/api/settings", strings.NewReader(body), nil)
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	if code, out := put(``); code != 200 || out["speedUnit"] != "bytes" {
		t.Fatalf("the default is bytes: %d %v", code, out["speedUnit"])
	}
	if code, out := put(`,"speedUnit":"bits"`); code != 200 || out["speedUnit"] != "bits" {
		t.Fatalf("bits not saved: %d %v", code, out["speedUnit"])
	}
	if code, out := put(``); code != 200 || out["speedUnit"] != "bits" {
		t.Fatalf("omitted must stay: %d %v", code, out["speedUnit"])
	}
	if code, _ := put(`,"speedUnit":"nibbles"`); code != 400 {
		t.Fatalf("an unknown unit must give 400, got %d", code)
	}
}

func TestLimitUnitsSetting(t *testing.T) {
	e := setup(t)
	put := func(extra string) (int, map[string]any) {
		body := `{"downLimitKBps":48828,"upLimitKBps":0,"altDownLimitKBps":61,"altUpLimitKBps":0,"ratioLimit":0,"maxActiveDownloads":0,"copyRemovePolicy":"with_data"` + extra + `}`
		r := e.do(t, "PUT", "/api/settings", strings.NewReader(body), nil)
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	if code, out := put(``); code != 200 || out["limitUnits"] != nil {
		t.Fatalf("no units by default: %d %v", code, out["limitUnits"])
	}
	code, out := put(`,"limitUnits":{"down":"Mbit","altDown":"kbit","up":""}`)
	units, _ := out["limitUnits"].(map[string]any)
	if code != 200 || units["down"] != "Mbit" || units["altDown"] != "kbit" || len(units) != 2 {
		t.Fatalf("units not saved (an empty one is dropped): %d %v", code, out["limitUnits"])
	}
	if code, out := put(``); code != 200 || len(out["limitUnits"].(map[string]any)) != 2 {
		t.Fatalf("omitted must stay: %d %v", code, out["limitUnits"])
	}
	if code, out := put(`,"limitUnits":{}`); code != 200 || out["limitUnits"] != nil {
		t.Fatalf("an empty set clears them: %d %v", code, out["limitUnits"])
	}
	for _, bad := range []string{`{"down":"Gbit"}`, `{"sideways":"KB"}`} {
		if code, _ := put(`,"limitUnits":` + bad); code != 400 {
			t.Fatalf("%s must give 400, got %d", bad, code)
		}
	}
}
