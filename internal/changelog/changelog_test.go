package changelog

import (
	"os"
	"regexp"
	"strings"
	"testing"

	equinox "github.com/Ozzvin/equinox"
)

func TestParse(t *testing.T) {
	md := "# Журнал изменений\n\nвступление\n\n## 1.0.2\n\n- второе\n- ещё\n\n## 1.0.1\r\n\r\n- первое\r\n"
	got := Parse(md)
	if len(got) != 2 || got[0].Version != "1.0.2" || got[1].Version != "1.0.1" {
		t.Fatalf("versions: %+v", got)
	}
	if got[0].Body != "- второе\n- ещё" || got[1].Body != "- первое" {
		t.Errorf("bodies: %q, %q", got[0].Body, got[1].Body)
	}
	if len(Parse("")) != 0 || len(Parse("# только заголовок\n")) != 0 {
		t.Error("no versions in, none out")
	}
}

// The changelog the program carries must be the one of this version: a release that forgot to write its entry
// would show a page whose newest version is the previous one.
func TestEmbeddedChangelogMatchesTheVersion(t *testing.T) {
	entries := Parse(equinox.Changelog)
	if len(entries) == 0 {
		t.Fatal("the embedded CHANGELOG.md has no versions")
	}
	num := regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	seen := map[string]bool{}
	for _, e := range entries {
		if !num.MatchString(e.Version) || seen[e.Version] || strings.TrimSpace(e.Body) == "" {
			t.Errorf("a bad entry: %q (repeated %v, body %d bytes)", e.Version, seen[e.Version], len(e.Body))
		}
		seen[e.Version] = true
	}
	raw, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if v := strings.TrimSpace(string(raw)); entries[0].Version != v {
		t.Errorf("the newest entry of CHANGELOG.md is %s, the version is %s", entries[0].Version, v)
	}
}
