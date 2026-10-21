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

// The description of the 1.1.0 release on GitHub, shortened: the update window shows only its lists, and on Windows
// without the lines for Docker and Umbrel.
func TestReleaseNotes(t *testing.T) {
	body := "## Equinox 1.1.0\r\n\r\n**Главное в версии: Equinox теперь работает в Docker.** Подробности.\r\n\r\n" +
		"**Windows** — установщик `Equinox-Setup-1.1.0.exe`.\r\n\r\n" +
		"### Новое\r\n\r\n- Только для Docker и Umbrel: «С сервера…» в окне добавления.\r\n\r\n" +
		"### Исправлено\r\n\r\n- Только для Docker и Umbrel: выбранные .torrent с сервера снова находятся.\r\n" +
		"- Пропавший файл больше не создаётся пустым.\r\n- [windows] Корневую папку можно свернуть,\r\n  и она не раскрывается.\r\n\r\n" +
		"Сборки для Windows не подписаны, поэтому SmartScreen может показать предупреждение.\r\n"
	for platform, want := range map[string]string{
		"windows": "### Исправлено\n\n- Пропавший файл больше не создаётся пустым.\n- Корневую папку можно свернуть,\n  и она не раскрывается.",
		"umbrel": "### Новое\n\n- «С сервера…» в окне добавления.\n\n### Исправлено\n\n- Выбранные .torrent с сервера снова находятся.\n" +
			"- Пропавший файл больше не создаётся пустым.",
	} {
		if got := ReleaseNotes(body, platform); got != want {
			t.Errorf("%s: got %q\nwant %q", platform, got, want)
		}
	}
	// the releases up to 1.0.47 are their lists alone; a description of another shape is only filtered by tags
	if got := ReleaseNotes("### Исправлено\n\n- правка\n", "windows"); got != "### Исправлено\n\n- правка" {
		t.Errorf("lists alone: %q", got)
	}
	if got := ReleaseNotes("- Fixed the tray menu.\n- [umbrel] server only\n", "windows"); got != "- Fixed the tray menu." {
		t.Errorf("another shape: %q", got)
	}
}

func TestBaseAndLess(t *testing.T) {
	for v, want := range map[string]string{"1.1.0-rc17": "1.1.0", "1.1.1-beta.2": "1.1.1", "beta-1.0.48": "1.0.48", "1.0.47": "1.0.47", "dev": "dev"} {
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
	// an rc or beta build (1.1.0-rc18, 1.1.1-beta.2) carries the entry of the version it leads to, as "installed" does
	if v := strings.TrimSpace(string(raw)); entries[0].Version != Base(v) {
		t.Errorf("the newest entry of CHANGELOG.md is %s, the version is %s", entries[0].Version, v)
	}
}
