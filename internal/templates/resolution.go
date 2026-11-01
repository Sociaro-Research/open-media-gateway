package templates

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var resolutionTier = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)([pk])$`)
var resolutionDimensions = regexp.MustCompile(`^([0-9]+)[x*×]([0-9]+)$`)

type resolutionSize struct {
	family string
	x, y   float64
}

func parseResolution(value string) resolutionSize {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
	if m := resolutionTier.FindStringSubmatch(value); m != nil {
		n, err := strconv.ParseFloat(m[1], 64)
		if err == nil && n > 0 && n <= 1e6 {
			return resolutionSize{family: m[2], x: n}
		}
	}
	if m := resolutionDimensions.FindStringSubmatch(value); m != nil {
		x, ex := strconv.ParseFloat(m[1], 64)
		y, ey := strconv.ParseFloat(m[2], 64)
		if ex == nil && ey == nil && x > 0 && y > 0 && x <= 1e6 && y <= 1e6 {
			return resolutionSize{family: "dimensions", x: x, y: y}
		}
	}
	return resolutionSize{}
}

// NearestResolution selects only advertised target values. Different semantic
// families are never guessed equivalent (for example K versus p). When numeric
// comparison is unavailable, the first choice is the existing Gateway default.
// Dimensions prefer orientation/aspect before pixel distance; explicit Ratio is
// independently validated and is never rewritten here.
func NearestResolution(preference string, choices []string) string {
	if len(choices) == 0 {
		return ""
	}
	if slices.Contains(choices, preference) {
		return preference
	}
	source := parseResolution(preference)
	if source.family == "" {
		return choices[0]
	}
	best := ""
	var bestScore [4]float64
	less := func(a, b [4]float64) bool {
		for i := range a {
			if math.Abs(a[i]-b[i]) > 1e-9 {
				return a[i] < b[i]
			}
		}
		return false
	}
	orientation := func(s resolutionSize) int {
		if s.x == s.y {
			return 0
		}
		if s.x > s.y {
			return 1
		}
		return -1
	}
	for _, choice := range choices {
		target := parseResolution(choice)
		if target.family != source.family {
			continue
		}
		score := [4]float64{0, 0, math.Abs(target.x - source.x), target.x}
		if source.family == "dimensions" {
			if orientation(source) != orientation(target) {
				score[0] = 1
			}
			score[1] = math.Abs(target.x/target.y - source.x/source.y)
			score[2] = math.Pow(target.x-source.x, 2) + math.Pow(target.y-source.y, 2)
			score[3] = target.x * target.y
		}
		if best == "" || less(score, bestScore) {
			best, bestScore = choice, score
		}
	}
	if best == "" {
		return choices[0]
	}
	return best
}
