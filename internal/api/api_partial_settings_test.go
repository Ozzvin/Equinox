package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ozzvin/equinox/internal/config"
)

// A settings request changes only what it names: the limits window sends no ratio, and the ratio set elsewhere stays.
// A request with one bad field changes nothing at all.
func TestSettingsChangeOnlyWhatIsSent(t *testing.T) {
	e := setup(t)
	put := func(body string) int {
		r := e.do(t, "PUT", "/api/settings", strings.NewReader(body), nil)
		r.Body.Close()
		return r.StatusCode
	}
	get := func() config.Settings {
		r := e.do(t, "GET", "/api/settings", nil, nil)
		defer r.Body.Close()
		var s config.Settings
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if c := put(`{"ratioLimit":2.5,"maxActiveDownloads":3,"copyRemovePolicy":"always"}`); c != 200 {
		t.Fatalf("setting the ratio: %d", c)
	}
	if c := put(`{"downLimitKBps":100}`); c != 200 {
		t.Fatalf("setting a limit alone: %d", c)
	}
	s := get()
	if s.DownLimitKBps != 100 || s.RatioLimit != 2.5 || s.MaxActiveDownloads != 3 || s.CopyRemovePolicy != "always" {
		t.Fatalf("what was not sent changed: %+v", s)
	}

	if c := put(`{"downLimitKBps":7,"maxActiveDownloads":-1}`); c != 400 {
		t.Fatalf("a negative number of downloads: %d", c)
	}
	if got := get().DownLimitKBps; got != 100 {
		t.Fatalf("a refused request changed the limit to %d", got)
	}
}
