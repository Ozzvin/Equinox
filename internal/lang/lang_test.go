package lang

import "testing"

func TestFromTags(t *testing.T) {
	for _, tc := range []struct {
		tags []string
		want string
	}{
		{[]string{"ru-RU", "en-US"}, "ru"},
		{[]string{"en-US", "ru-RU"}, "en"}, // the first one counts
		{[]string{"de-DE"}, "en"},
		{[]string{"RU"}, "ru"},
		{[]string{"", "ru-RU"}, "ru"},
		{nil, "ru"},
	} {
		if got := FromTags(tc.tags); got != tc.want {
			t.Errorf("FromTags(%v) = %q, want %q", tc.tags, got, tc.want)
		}
	}
}

func TestCurrentFollowsTheChoice(t *testing.T) {
	defer SetPreference(nil)
	pref := ""
	SetPreference(func() string { return pref })
	for _, tc := range []struct{ pref, want string }{{"en", "en"}, {"ru", "ru"}} {
		pref = tc.pref
		if got := Current(); got != tc.want {
			t.Errorf("preference %q gave %q, want %q", tc.pref, got, tc.want)
		}
	}
	pref = ""
	if got := Current(); got != System() {
		t.Errorf("no choice must follow the system (%q), got %q", System(), got)
	}
}

func TestTrAndLimitDetail(t *testing.T) {
	defer SetPreference(nil)
	lang := "ru"
	SetPreference(func() string { return lang })
	if got := Tr("Выход"); got != "Выход" {
		t.Errorf("Russian must stay as it is: %q", got)
	}
	if got := LimitDetail("рейтинг 2.00", "limit.ratio", []string{"2.00"}); got != "рейтинг 2.00" {
		t.Errorf("Russian detail must stay: %q", got)
	}
	lang = "en"
	if got := Tr("Выход"); got != "Exit" {
		t.Errorf("Tr = %q", got)
	}
	if got := Tr("текст без перевода"); got != "текст без перевода" {
		t.Errorf("a text with no English stays: %q", got)
	}
	for _, tc := range []struct {
		code string
		args []string
		want string
	}{
		{"limit.ratio", []string{"2.00"}, "ratio 2.00"},
		{"limit.time", []string{"3", "d"}, "seeding time 3 d"},
		{"limit.time", []string{"90", "min"}, "seeding time 90 min"},
		{"limit.unknown", nil, "русский текст"}, // a code not known here: the Russian text
		{"", nil, "русский текст"},
	} {
		if got := LimitDetail("русский текст", tc.code, tc.args); got != tc.want {
			t.Errorf("LimitDetail(%q, %v) = %q, want %q", tc.code, tc.args, got, tc.want)
		}
	}
}
