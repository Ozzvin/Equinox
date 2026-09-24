package core

import (
	"net"
	"testing"
)

func TestSelfIPsVoteAndFilter(t *testing.T) {
	var s selfIPs
	if v4, v6 := s.best(); v4 != "" || v6 != "" {
		t.Fatalf("nothing reported yet, got %q %q", v4, v6)
	}
	// Addresses nobody outside could reach us at are not our address.
	for _, bad := range []string{"192.168.1.5", "10.0.0.2", "127.0.0.1", "100.72.1.9", "169.254.3.3", "0.0.0.0"} {
		s.note(net.ParseIP(bad))
	}
	s.note(nil)
	s.note(net.IP{1, 2, 3})
	if v4, _ := s.best(); v4 != "" {
		t.Fatalf("private or malformed reports must be ignored, got %q", v4)
	}

	// One peer that is wrong is outvoted.
	s.note(net.ParseIP("198.51.100.9"))
	for i := 0; i < 3; i++ {
		s.note(net.ParseIP("203.0.113.7"))
	}
	s.note(net.ParseIP("2001:db8::7"))
	// An IPv4 address as 16 bytes (how the wire format may hand it over) counts as IPv4.
	s.note(net.ParseIP("203.0.113.7").To16())
	v4, v6 := s.best()
	if v4 != "203.0.113.7" {
		t.Errorf("v4 = %q, want the one reported most often", v4)
	}
	if v6 != "2001:db8::7" {
		t.Errorf("v6 = %q", v6)
	}
}

func TestSelfIPsOnlyRememberTheLatestReports(t *testing.T) {
	var s selfIPs
	for i := 0; i < selfIPWindow; i++ {
		s.note(net.ParseIP("203.0.113.1"))
	}
	for i := 0; i < selfIPWindow; i++ {
		s.note(net.ParseIP("203.0.113.2")) // the address changed: the old reports must fade out
	}
	if v4, _ := s.best(); v4 != "203.0.113.2" {
		t.Fatalf("after the provider changed the address, got %q", v4)
	}
}
