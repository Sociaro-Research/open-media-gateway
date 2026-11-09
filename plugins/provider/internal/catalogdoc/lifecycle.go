package catalogdoc

import (
	"strings"
	"time"
)

// Row preserves escaped pipes inside a Markdown table cell.
func Row(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimSpace(strings.TrimSuffix(line, `\`))
	if !strings.HasPrefix(line, "|") {
		return nil
	}
	line = strings.ReplaceAll(line, `\|`, "\x00")
	cells := strings.Split(strings.Trim(line, "|"), "|")
	for i := range cells {
		cells[i] = Plain(strings.ReplaceAll(cells[i], "\x00", "|"))
	}
	return cells
}

func Date(text string) string {
	for _, layout := range []string{"2006-01-02", "January 2, 2006", "Jan 2, 2006"} {
		if t, err := time.Parse(layout, strings.TrimSpace(text)); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return ""
}
