package core

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ozzvin/equinox/internal/portmap"
)

func TestPortReportAdviceHasACode(t *testing.T) {
	now := time.Now()
	for name, tc := range map[string]struct {
		st      portmap.Status
		public  int64
		unknown bool
		code    string
		args    []string
	}{
		"open":           {portmap.Status{Enabled: true, Mapped: true}, 3, false, "port.open", nil},
		"cgnat":          {portmap.Status{Enabled: true, ExternalIP: "10.1.2.3"}, 0, false, "port.cgnat", []string{"10.1.2.3"}},
		"manual":         {portmap.Status{}, 0, false, "port.manual", nil},
		"mapped":         {portmap.Status{Enabled: true, Mapped: true}, 0, false, "port.mapped", nil},
		"mapped unknown": {portmap.Status{Enabled: true, Mapped: true}, 0, true, "port.mapped_unknown", nil},
		"closed":         {portmap.Status{Enabled: true, Failures: 2}, 0, false, "port.closed", nil},
		"closed answer":  {portmap.Status{Enabled: true, Failures: 2, LastError: "SOAP fault"}, 0, false, "port.closed_answer", []string{"SOAP fault"}},
		"checking":       {portmap.Status{Enabled: true}, 0, false, "port.checking", nil},
	} {
		r := makeReport(tc.st, tc.public, now, tc.unknown, now, 0)
		if r.AdviceCode != tc.code || r.Advice == "" {
			t.Errorf("%s: code %q (advice %q), want %q", name, r.AdviceCode, r.Advice, tc.code)
		}
		if len(r.AdviceArgs) != len(tc.args) || (len(tc.args) == 1 && r.AdviceArgs[0] != tc.args[0]) {
			t.Errorf("%s: args %v, want %v", name, r.AdviceArgs, tc.args)
		}
	}
}

func TestLimitReasonHasACode(t *testing.T) {
	for _, tc := range []struct {
		kind string
		v    float64
		text string
		code string
		args []string
	}{
		{"ratio", 2, "рейтинг 2.00", "limit.ratio", []string{"2.00"}},
		{"time", 4320, "время раздачи 3 дн.", "limit.time", []string{"3", "d"}},
		{"time", 720, "время раздачи 12 ч", "limit.time", []string{"12", "h"}},
		{"time", 90, "время раздачи 90 мин", "limit.time", []string{"90", "min"}},
	} {
		text, code, args := limitReason(tc.kind, tc.v)
		if text != tc.text || code != tc.code || len(args) != len(tc.args) || args[0] != tc.args[0] || (len(args) == 2 && args[1] != tc.args[1]) {
			t.Errorf("limitReason(%s, %v) = %q %q %v, want %q %q %v", tc.kind, tc.v, text, code, args, tc.text, tc.code, tc.args)
		}
	}
}

func TestDeleteErrorHasACode(t *testing.T) {
	cause := errors.New("access denied")
	err := deleteError(filepath.Join("data", "film.mkv"), cause)
	var c Coder
	if !errors.As(err, &c) {
		t.Fatal("the error must be a Coder")
	}
	if code, args := c.ErrCode(); code != "file.delete" || len(args) != 2 || args[0] != "film.mkv" || args[1] != "access denied" {
		t.Errorf("code %q args %v", code, args)
	}
	if !errors.Is(err, cause) {
		t.Error("the cause must stay reachable")
	}
	if err.Error() != "не удалось удалить «film.mkv»: access denied" {
		t.Errorf("the Russian text changed: %q", err.Error())
	}
	busy := deleteError("film.mkv", errors.New("The process cannot access the file because it is being used by another process"))
	if errors.As(busy, &c); c == nil {
		t.Fatal("a busy file must be a Coder too")
	} else if code, args := c.ErrCode(); code != "file.busy" || len(args) != 1 || args[0] != "film.mkv" {
		t.Errorf("busy: code %q args %v", code, args)
	}
}
