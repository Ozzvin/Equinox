package desktop

import "fmt"

// FormatRate writes a speed given in bytes per second, either as bytes (КБ/с, МБ/с: binary prefixes, like
// sizes) or as bits (Кб/с, Мб/с: decimal prefixes, the way networks are measured).
func FormatRate(bytesPerSec int64, bits bool) string { return FormatRateIn(bytesPerSec, bits, false) }

// FormatRateIn is FormatRate in Russian (english false) or in English (KB/s, Mb/s).
func FormatRateIn(bytesPerSec int64, bits, english bool) string {
	u := func(ru, en string) string {
		if english {
			return en
		}
		return ru
	}
	if bits {
		v := float64(bytesPerSec) * 8
		switch {
		case v >= 1e9:
			return fmt.Sprintf("%.1f %s", v/1e9, u("Гб/с", "Gb/s"))
		case v >= 1e6:
			return fmt.Sprintf("%.1f %s", v/1e6, u("Мб/с", "Mb/s"))
		case v >= 1e3:
			return fmt.Sprintf("%.0f %s", v/1e3, u("Кб/с", "Kb/s"))
		}
		return fmt.Sprintf("%.0f %s", v, u("б/с", "b/s"))
	}
	switch {
	case bytesPerSec >= 1<<20:
		return fmt.Sprintf("%.1f %s", float64(bytesPerSec)/(1<<20), u("МБ/с", "MB/s"))
	case bytesPerSec >= 1<<10:
		return fmt.Sprintf("%.0f %s", float64(bytesPerSec)/(1<<10), u("КБ/с", "KB/s"))
	}
	return fmt.Sprintf("%d %s", bytesPerSec, u("Б/с", "B/s"))
}
