package catalogsync

import "strconv"

func bytePlusModelChildHeader(parents, cells []string) bool {
	if cells[0] == "" {
		return true
	}
	if len(cells) != len(parents) || cells[0] != "Model ID" {
		return false
	}
	// HTML rowspan expansion repeats standalone headers in the child row.
	// An identical row is a new/repeated top-level header, not its child.
	different, standalone := false, false
	for i := 1; i < len(parents); i++ {
		different = different || cells[i] != parents[i]
		if parents[i] == "" || i > 1 && parents[i] == parents[i-1] || i+1 < len(parents) && (parents[i+1] == "" || parents[i+1] == parents[i]) {
			continue
		}
		standalone = standalone || cells[i] == "" || cells[i] == parents[i]
	}
	return different && standalone
}

// Preserve grouped table headings as review evidence, without interpreting
// their values or mapping unfamiliar fields to accepted capability facts.
func bytePlusGroupedModelColumns(parents, children []string) []string {
	columns := append([]string(nil), parents...)
	groups := append([]string(nil), parents...)
	counts := map[string]int{}
	parent := ""
	for i := 1; i < len(groups); i++ {
		if groups[i] != "" {
			parent = groups[i]
		}
		groups[i] = parent
		counts[parent]++
	}
	for i := 1; i < len(columns); i++ {
		group := groups[i]
		if group == "" {
			group = "Column " + strconv.Itoa(i+1)
		}
		child := ""
		if len(children) == len(parents) {
			child = children[i]
		}
		columns[i] = group
		if child != "" && child != group {
			columns[i] += " / " + child
		} else if counts[groups[i]] > 1 {
			// A missing child has no inferred unit or meaning. Keep columns
			// distinct so reconciliation cannot overwrite their evidence.
			columns[i] += " / Column " + strconv.Itoa(i+1)
		}
	}
	return columns
}
