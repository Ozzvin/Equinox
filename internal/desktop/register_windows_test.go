//go:build windows

package desktop

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// All registry work in these tests happens under a scratch branch that is deleted again,
// so the developer's real startup list and default apps are never touched.
const scratch = `Software\EquinoxTest`

func deleteTree(path string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return
	}
	subs, _ := k.ReadSubKeyNames(-1)
	k.Close()
	for _, s := range subs {
		deleteTree(path + `\` + s)
	}
	_ = registry.DeleteKey(registry.CURRENT_USER, path)
}

func TestAutostartRoundTrip(t *testing.T) {
	t.Cleanup(func() { deleteTree(scratch) })
	if autostartEnabled(scratch) {
		t.Fatal("scratch branch must start empty")
	}
	if err := setAutostart(scratch, true, true); err != nil {
		t.Fatal(err)
	}
	if !autostartEnabled(scratch) {
		t.Fatal("autostart not reported after enabling")
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, scratch+`\`+runSuffix, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	v, _, _ := k.GetStringValue(runName)
	k.Close()
	if !strings.HasPrefix(v, `"`) || !strings.HasSuffix(v, `" -hidden`) {
		t.Fatalf("run command must be a quoted path plus -hidden: %s", v)
	}
	if err := setAutostart(scratch, true, false); err != nil { // without -hidden
		t.Fatal(err)
	}
	k2, _ := registry.OpenKey(registry.CURRENT_USER, scratch+``+runSuffix, registry.QUERY_VALUE)
	v2, _, _ := k2.GetStringValue(runName)
	k2.Close()
	if strings.HasSuffix(v2, "-hidden") {
		t.Fatalf("visible start must not carry -hidden: %s", v2)
	}
	if err := setAutostart(scratch, false, false); err != nil {
		t.Fatal(err)
	}
	if autostartEnabled(scratch) {
		t.Fatal("autostart still reported after disabling")
	}
	if err := setAutostart(scratch, false, false); err != nil { // disabling twice is harmless
		t.Fatalf("second disable: %v", err)
	}
}

func TestRegisterHandlersWritesExpectedKeys(t *testing.T) {
	t.Cleanup(func() { deleteTree(scratch) })
	if err := registerHandlers(scratch); err != nil {
		t.Fatal(err)
	}
	read := func(path, name string) string {
		k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		defer k.Close()
		v, _, err := k.GetStringValue(name)
		if err != nil {
			t.Fatalf("%s[%s]: %v", path, name, err)
		}
		return v
	}
	cmd := read(scratch+`\Classes\Equinox.Magnet\shell\open\command`, "")
	if !strings.HasSuffix(cmd, `" "%1"`) {
		t.Fatalf("handler command must pass the link as %%1: %s", cmd)
	}
	_ = read(scratch+`\Classes\Equinox.Magnet`, "URL Protocol") // marks it as a URL protocol
	if got := read(scratch+`\Equinox\Capabilities\URLAssociations`, "magnet"); got != "Equinox.Magnet" {
		t.Fatalf("magnet association: %s", got)
	}
	if got := read(scratch+`\Equinox\Capabilities\FileAssociations`, ".torrent"); got != "Equinox.Torrent" {
		t.Fatalf(".torrent association: %s", got)
	}
	if got := read(scratch+`\RegisteredApplications`, "Equinox"); got != scratch+`\Equinox\Capabilities` {
		t.Fatalf("registered application must point at the capabilities key: %s", got)
	}
}
