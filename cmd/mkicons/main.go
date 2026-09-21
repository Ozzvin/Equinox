// Command mkicons writes the application icon as PNG files for the Windows resource
// compiler. Run it (and go-winres) again only when the icon design changes.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Ozzvin/equinox/internal/desktop"
)

func main() {
	dir := filepath.Join("cmd", "equinox", "winres")
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	for _, n := range []int{16, 32, 48, 256} {
		p := filepath.Join(dir, fmt.Sprintf("icon%d.png", n))
		if err := os.WriteFile(p, desktop.PNG(n), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("wrote", p)
	}
	// The installer takes a .ico; the tray icon is one already.
	ico := filepath.Join("installer", "equinox.ico")
	if err := os.MkdirAll("installer", 0o755); err == nil {
		err = os.WriteFile(ico, desktop.Icon(), 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("wrote", ico)
	}
}
