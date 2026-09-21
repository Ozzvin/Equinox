package desktop

import "fmt"

// FormatRate writes a speed given in bytes per second, either as bytes (КБ/с, МБ/с: binary prefixes, like
// sizes) or as bits (Кб/с, Мб/с: decimal prefixes, the way networks are measured).
func FormatRate(bytesPerSec int64, bits bool) string {
	if bits {
		v := float64(bytesPerSec) * 8
		switch {
		case v >= 1e9:
			return fmt.Sprintf("%.1f Гб/с", v/1e9)
		case v >= 1e6:
			return fmt.Sprintf("%.1f Мб/с", v/1e6)
		case v >= 1e3:
			return fmt.Sprintf("%.0f Кб/с", v/1e3)
		}
		return fmt.Sprintf("%.0f б/с", v)
	}
	switch {
	case bytesPerSec >= 1<<20:
		return fmt.Sprintf("%.1f МБ/с", float64(bytesPerSec)/(1<<20))
	case bytesPerSec >= 1<<10:
		return fmt.Sprintf("%.0f КБ/с", float64(bytesPerSec)/(1<<10))
	}
	return fmt.Sprintf("%d Б/с", bytesPerSec)
}
