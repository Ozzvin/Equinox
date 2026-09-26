// Package lang is the language of what the program itself says outside the web page: the tray menu, the messages
// of Windows and the notifications. The page has its own way (i18n.js); both follow the same choice of the person:
// Settings → Interface → Language, kept as settings.Language ("" as in the system, "ru", "en").
package lang

import "strings"

var preference func() string

// SetPreference tells where the choice of the person is read from (the settings); it is asked each time, so a change
// in the settings is seen at once.
func SetPreference(f func() string) { preference = f }

// Current is "ru" or "en": the choice of the person, or, when there is none, the language of the system.
func Current() string {
	p := ""
	if preference != nil {
		p = preference()
	}
	if p == "ru" || p == "en" {
		return p
	}
	return System()
}

// English tells whether the language is English.
func English() bool { return Current() == "en" }

// FromTags picks the language of a list of language tags of the system ("ru-RU", "en-US"), the first one counting:
// Russian if it begins with "ru", English otherwise. With no tags at all it is Russian, the language the
// program was written in.
func FromTags(tags []string) string {
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "ru") {
			return "ru"
		}
		return "en"
	}
	return "ru"
}

// Tr is the text in the current language. The texts are written in Russian; the English of each is here.
// A text that has no English stays as it is.
func Tr(ru string) string {
	if !English() {
		return ru
	}
	if en, ok := english[ru]; ok {
		return en
	}
	return ru
}

// LimitDetail translates the reason a torrent stopped seeding, as the engine words it in Russian:
// "рейтинг 2.00", "время раздачи 3 дн.".
func LimitDetail(detail string) string {
	if !English() {
		return detail
	}
	if v, ok := strings.CutPrefix(detail, "рейтинг "); ok {
		return "ratio " + v
	}
	if v, ok := strings.CutPrefix(detail, "время раздачи "); ok {
		for _, u := range [][2]string{{" дн.", " d"}, {" ч", " h"}, {" мин", " min"}} {
			if s, ok := strings.CutSuffix(v, u[0]); ok {
				v = s + u[1]
				break
			}
		}
		return "seeding time " + v
	}
	return detail
}

var english = map[string]string{
	// the tray menu
	"Открыть Equinox":                             "Open Equinox",
	"Показать окно":                               "Show the window",
	"Ограничение скорости":                        "Speed limit",
	"Режим «черепаха»":                            "Turtle mode",
	"Запускать вместе с Windows":                  "Start with Windows",
	"Запуск в трее при входе в систему":           "Start in the tray when you sign in",
	"Сделать торрент-клиентом по умолчанию…":      "Make the default torrent client…",
	"Выбрать Equinox в «Приложения по умолчанию»": "Choose Equinox in “Default apps”",
	"Выход": "Exit",
	"Остановить все раздачи и выйти": "Stop all torrents and exit",
	// the windows and messages
	"Добавить раздачу":                                                      "Add torrent",
	"Не удалось изменить автозапуск:":                                       "Could not change the autostart:",
	"Не удалось зарегистрировать приложение:":                               "Could not register the application:",
	"Не удалось запустить Equinox:":                                         "Could not start Equinox:",
	"Не удалось открыть окно: не найден Microsoft Edge WebView2 Runtime.":   "Could not open the window: Microsoft Edge WebView2 Runtime is not found.",
	"Приложение продолжает работать в трее, интерфейс доступен в браузере:": "The application keeps working in the tray, the interface is available in the browser:",
	// notifications
	"Загрузка завершена":  "Download finished",
	"Раздача остановлена": "Seeding stopped",
	"достигнут лимит:":    "limit reached:",
}
