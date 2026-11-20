package core

import "time"

func reportCivilDate(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ReportDayStart uses the civil year/month/day encoded by date, not its
// instant. It selects the earlier midnight when midnight repeats, the first
// valid wall time after a gap, and the next valid day if the date is skipped.
// Report filters are contiguous instant ranges; they do not split dates
// around historical transitions that briefly return to a previous date.
func ReportDayStart(date time.Time, zone *time.Location) time.Time {
	midnight := reportCivilDate(date)
	// All IANA offsets are under 24 hours. Include both sides of transitions
	// so time.Date's unspecified choice at a gap or overlap is not involved.
	for instant := midnight.Add(-24 * time.Hour).In(zone); ; {
		_, offset := instant.Zone()
		_, end := instant.ZoneBounds()
		if !end.IsZero() && !end.After(instant) {
			// Go's POSIX timezone fallback can return an artificial
			// year-end bound one day early in leap years.
			end = time.Date(instant.UTC().Year()+1, time.January, 1, 0, 0, 0, 0, time.UTC)
		}
		candidate := midnight.Add(-time.Duration(offset) * time.Second)
		if candidate.Before(instant) {
			candidate = instant
		}
		if end.IsZero() || candidate.Before(end) {
			return candidate.In(zone)
		}
		instant = end.In(zone)
	}
}
