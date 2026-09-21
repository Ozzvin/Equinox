package api

import (
	"bytes"
	"strings"
	"testing"
)

func TestFileOpenAndDeleteEndpointsRefuseWhatTheyShould(t *testing.T) {
	e := setup(t)
	ghost := strings.Repeat("a", 40)
	for name, tc := range map[string]struct {
		path, body string
		want       int
	}{
		"open, unknown torrent":  {"/api/torrents/" + ghost + "/files/0/open", ``, 404},
		"open, index not number": {"/api/torrents/" + ghost + "/files/x/open", ``, 400},
		"delete, unknown":        {"/api/torrents/" + ghost + "/files/delete", `{"files":[0]}`, 404},
		"delete, no files":       {"/api/torrents/" + ghost + "/files/delete", `{"files":[]}`, 400},
		"delete, garbage":        {"/api/torrents/" + ghost + "/files/delete", `nope`, 400},
	} {
		r := e.do(t, "POST", tc.path, bytes.NewReader([]byte(tc.body)), nil)
		r.Body.Close()
		if r.StatusCode != tc.want {
			t.Errorf("%s: %d, want %d", name, r.StatusCode, tc.want)
		}
	}
}
