package changelog

import (
	"os"
	"regexp"
	"slices"
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

func TestFor(t *testing.T) {
	md := "# Журнал\n\n## 1.1.1\n\n### Новое\n\n- общее\n- [windows] только windows\n  продолжение windows\n- [umbrel,linux] серверам\n\n" +
		"### Исправлено\n\n- [windows] правка windows\n\n## 1.1.0\n\n### Исправлено\n\n- [umbrel] правка umbrel\n\n" +
		"## 1.0.47\n\n- старое\n"
	entries := Parse(md)
	for _, c := range []struct {
		platform string
		want     []Entry
	}{
		{"windows", []Entry{
			{"1.1.1", "### Новое\n\n- общее\n- только windows\n  продолжение windows\n\n### Исправлено\n\n- правка windows"},
			{"1.0.47", "- старое"},
		}},
		{"umbrel", []Entry{
			{"1.1.1", "### Новое\n\n- общее\n- серверам"},
			{"1.1.0", "### Исправлено\n\n- правка umbrel"},
		}},
		{"linux", []Entry{
			{"1.1.1", "### Новое\n\n- общее\n- серверам"},
		}},
	} {
		got := For(entries, c.platform)
		if len(got) != len(c.want) {
			t.Errorf("%s: %+v", c.platform, got)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %q\nwant %q", c.platform, got[i], c.want[i])
			}
		}
	}
}

func TestBaseAndLess(t *testing.T) {
	for v, want := range map[string]string{"1.1.0-rc17": "1.1.0", "beta-1.0.48": "1.0.48", "1.0.47": "1.0.47", "dev": "dev"} {
		if got := Base(v); got != want {
			t.Errorf("Base(%q) = %q, want %q", v, got, want)
		}
	}
	if !Less("1.0.47", "1.1.0") || !Less("1.0.9", "1.0.10") || Less("1.1.0-rc17", "1.1.0") || Less("1.1.0", "1.0.47") {
		t.Error("Less")
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
		for _, line := range strings.Split(e.Body, "\n") {
			for _, tag := range Tags(line) {
				if !slices.Contains(Platforms, tag) {
					t.Errorf("%s: an unknown tag [%s] in %q", e.Version, tag, line)
				}
			}
		}
	}
	raw, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if v := strings.TrimSpace(string(raw)); entries[0].Version != v {
		t.Errorf("the newest entry of CHANGELOG.md is %s, the version is %s", entries[0].Version, v)
	}
}
