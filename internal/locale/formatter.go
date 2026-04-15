package locale

import "time"

func FormatTime(t time.Time, locale string) string {
	return t.In(loadLocalOrUTC()).Format(timeLayout(locale))
}

func FormatDate(t time.Time, locale string) string {
	return t.In(loadLocalOrUTC()).Format(dateLayout(locale))
}

func loadLocalOrUTC() *time.Location {
	loc, err := time.LoadLocation("Local")
	if err != nil {
		return time.UTC
	}
	return loc
}

func timeLayout(locale string) string {
	switch locale {
	case "ko":
		return "2006년 1월 2일 15:04"
	case "ja", "zh":
		return "2006年1月2日 15:04"
	default:
		return "2006-01-02 15:04 MST"
	}
}

func dateLayout(locale string) string {
	switch locale {
	case "ko":
		return "2006년 1월 2일"
	case "ja", "zh":
		return "2006年1月2日"
	default:
		return "2006-01-02"
	}
}
