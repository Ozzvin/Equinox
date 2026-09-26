//go:build !windows

package lang

import "os"

// System is the language of the system, from the usual variables of the environment.
func System() string {
	for _, v := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if s := os.Getenv(v); s != "" {
			return FromTags([]string{s})
		}
	}
	return "ru"
}
