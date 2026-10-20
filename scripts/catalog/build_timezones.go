//go:build ignore

// Generate compact civil-time choices from the same IANA database Go ships.
// Run from the repository root: go run scripts/catalog/build_timezones.go
package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type group struct {
	Offset int      `json:"offset"`
	Cities []string `json:"cities"`
	Zones  []string `json:"zones"`
}

// Prefer recognizable cities to historical aliases or small settlements.
var examples = []struct{ zone, city string }{
	{"UTC", "Reykjavik"}, {"Africa/Accra", "Accra"},
	{"America/Los_Angeles", "Los Angeles"}, {"America/Vancouver", "Vancouver"},
	{"America/New_York", "New York"}, {"America/Toronto", "Toronto"},
	{"America/Chicago", "Chicago"}, {"America/Mexico_City", "Mexico City"},
	{"America/Denver", "Denver"}, {"America/Phoenix", "Phoenix"},
	{"Pacific/Honolulu", "Honolulu"}, {"America/Anchorage", "Anchorage"},
	{"Pacific/Pago_Pago", "Pago Pago"}, {"Pacific/Niue", "Alofi"},
	{"Pacific/Pitcairn", "Adamstown"}, {"Pacific/Gambier", "Gambier Islands"},
	{"America/Halifax", "Halifax"}, {"America/St_Johns", "St. John's"},
	{"America/Sao_Paulo", "São Paulo"}, {"America/Argentina/Buenos_Aires", "Buenos Aires"},
	{"America/Bogota", "Bogotá"}, {"America/Lima", "Lima"}, {"America/Caracas", "Caracas"},
	{"America/Santiago", "Santiago"}, {"America/Havana", "Havana"},
	{"Pacific/Easter", "Hanga Roa"}, {"America/Nuuk", "Nuuk"},
	{"Europe/London", "London"}, {"Europe/Lisbon", "Lisbon"}, {"Europe/Dublin", "Dublin"},
	{"Europe/Paris", "Paris"}, {"Europe/Berlin", "Berlin"}, {"Europe/Rome", "Rome"},
	{"Europe/Athens", "Athens"}, {"Asia/Nicosia", "Nicosia"}, {"Europe/Bucharest", "Bucharest"},
	{"Europe/Moscow", "Moscow"}, {"Europe/Istanbul", "Istanbul"}, {"Asia/Riyadh", "Riyadh"},
	{"Africa/Cairo", "Cairo"}, {"Africa/Johannesburg", "Johannesburg"}, {"Africa/Harare", "Harare"},
	{"Africa/Lagos", "Lagos"}, {"Africa/Kinshasa", "Kinshasa"}, {"Africa/Casablanca", "Casablanca"},
	{"Asia/Dubai", "Dubai"}, {"Asia/Muscat", "Muscat"}, {"Asia/Baku", "Baku"},
	{"Asia/Tehran", "Tehran"}, {"Asia/Kabul", "Kabul"}, {"Asia/Karachi", "Karachi"},
	{"Asia/Tashkent", "Tashkent"}, {"Asia/Kolkata", "New Delhi"}, {"Asia/Colombo", "Colombo"},
	{"Asia/Kathmandu", "Kathmandu"}, {"Asia/Dhaka", "Dhaka"}, {"Asia/Yangon", "Yangon"},
	{"Asia/Bangkok", "Bangkok"}, {"Asia/Jakarta", "Jakarta"}, {"Asia/Ho_Chi_Minh", "Ho Chi Minh City"},
	{"Asia/Shanghai", "Beijing"}, {"Asia/Singapore", "Singapore"}, {"Asia/Hong_Kong", "Hong Kong"},
	{"Asia/Tokyo", "Tokyo"}, {"Asia/Seoul", "Seoul"},
	{"Australia/Sydney", "Sydney"}, {"Australia/Melbourne", "Melbourne"},
	{"Australia/Brisbane", "Brisbane"}, {"Australia/Adelaide", "Adelaide"},
	{"Australia/Darwin", "Darwin"}, {"Australia/Perth", "Perth"},
	{"Australia/Lord_Howe", "Lord Howe Island"}, {"Pacific/Noumea", "Nouméa"},
	{"Pacific/Guadalcanal", "Honiara"},
	{"Pacific/Auckland", "Auckland"}, {"Pacific/Fiji", "Suva"},
	{"Pacific/Chatham", "Chatham Islands"}, {"Pacific/Kiritimati", "Kiritimati"},
	{"Pacific/Apia", "Apia"}, {"Pacific/Tongatapu", "Nukuʻalofa"},
}

func main() {
	archive, err := zip.OpenReader(filepath.Join(runtime.GOROOT(), "lib/time/zoneinfo.zip"))
	must(err)
	defer archive.Close()
	start := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(10, 0, 0)
	groups := map[string]*group{}
	for _, file := range archive.File {
		if strings.HasSuffix(file.Name, "/") || file.Name == "Factory" {
			continue
		}
		reader, err := file.Open()
		must(err)
		data, err := io.ReadAll(reader)
		reader.Close()
		must(err)
		loc, err := time.LoadLocationFromTZData(file.Name, data)
		must(err)
		var profile strings.Builder
		_, minimum := start.In(loc).Zone()
		previous := minimum
		fmt.Fprintf(&profile, "%d;", previous)
		// Future civil-time transitions are more than a day apart. Sample each
		// day, then locate each change to the second; avoid ZoneBounds' synthetic
		// year-end boundaries in POSIX extrapolation after explicit TZif records.
		for at := start.Add(24 * time.Hour); !at.After(end); at = at.Add(24 * time.Hour) {
			_, offset := at.In(loc).Zone()
			if at.Before(start.AddDate(1, 0, 0)) && offset < minimum {
				minimum = offset
			}
			if offset != previous {
				low, high := at.Add(-24*time.Hour).Unix(), at.Unix()
				for high-low > 1 {
					mid := low + (high-low)/2
					_, current := time.Unix(mid, 0).In(loc).Zone()
					if current == previous {
						low = mid
					} else {
						high = mid
					}
				}
				fmt.Fprintf(&profile, "%d:%d;", high, offset)
				previous = offset
			}
		}
		key := profile.String()
		if groups[key] == nil {
			groups[key] = &group{Offset: minimum / 60, Cities: []string{}}
		}
		groups[key].Zones = append(groups[key].Zones, file.Name)
	}
	choices := []*group{}
	for _, g := range groups {
		sort.Strings(g.Zones)
		for _, e := range examples {
			if contains(g.Zones, e.zone) && len(g.Cities) < 3 {
				g.Cities = append(g.Cities, e.city)
				// The first example is the representative for a newly selected group.
				if len(g.Cities) == 1 {
					g.Zones = append([]string{e.zone}, without(g.Zones, e.zone)...)
				}
			}
		}
		if len(g.Cities) == 0 {
			for _, zone := range g.Zones {
				if strings.Contains(zone, "/") && !strings.HasPrefix(zone, "Etc/") {
					parts := strings.Split(zone, "/")
					g.Cities = append(g.Cities, strings.ReplaceAll(parts[len(parts)-1], "_", " "))
					break
				}
			}
		}
		if len(g.Cities) == 0 && g.Offset == -720 {
			g.Cities = []string{"Baker Island", "Howland Island"}
		}
		choices = append(choices, g)
	}
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].Offset != choices[j].Offset {
			return choices[i].Offset < choices[j].Offset
		}
		return strings.Join(choices[i].Cities, ", ") < strings.Join(choices[j].Cities, ", ")
	})
	data, err := json.Marshal(choices)
	must(err)
	output := `// Generated by go run scripts/catalog/build_timezones.go. Do not edit the data.
// Equivalent IANA zones share offset transitions from 2026-10-09 to 2036-10-09.
(() => {
 'use strict';
 const groups = ` + string(data) + `;
 const offset = minutes => 'UTC'+(minutes<0?'-':'+')+String(Math.floor(Math.abs(minutes)/60)).padStart(2,'0')+':'+String(Math.abs(minutes)%60).padStart(2,'0');
 const title = group => offset(group.offset)+(group.cities.length?' ('+group.cities.join(', ')+')':'');
 function options(selected){
  const choices=groups.map(group=>({value:group.zones.includes(selected)?selected:group.zones[0],label:title(group)}));
  // Preserve valid legacy/custom IANA identifiers even across database upgrades.
  if(selected&&!groups.some(group=>group.zones.includes(selected))){
   const formatter=new Intl.DateTimeFormat('en',{timeZone:selected,timeZoneName:'shortOffset'});
   const name=formatter.formatToParts(new Date()).find(part=>part.type==='timeZoneName').value.replace('GMT','UTC');
   choices.push({value:selected,label:name+' ('+selected.split('/').at(-1).replaceAll('_',' ')+')'});
  }
  return choices;
 }
 function label(zone){return options(zone).find(choice=>choice.value===zone)?.label||zone;}
 window.Timezones={options,label};
})();
`
	must(os.WriteFile("web/public/ui/timezones.js", []byte(output), 0644))
	fmt.Printf("Generated %d timezone groups\n", len(choices))
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func without(values []string, value string) []string {
	result := []string{}
	for _, v := range values {
		if v != value {
			result = append(result, v)
		}
	}
	return result
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
