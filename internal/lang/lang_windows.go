//go:build windows

package lang

import "golang.org/x/sys/windows"

// System is the language of the system: the first of the languages the person prefers for Windows.
func System() string {
	tags, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return "ru"
	}
	return FromTags(tags)
}
