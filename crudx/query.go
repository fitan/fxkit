package crudx

import "strings"

// ParseSort parses "col desc", "col,desc" or "col" (asc).
func ParseSort(spec string) (field, dir string) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", ""
	}
	parts := strings.Fields(spec)
	field = parts[0]
	dir = "asc"
	if len(parts) > 1 {
		switch strings.ToLower(parts[1]) {
		case "desc", "descending", "-":
			dir = "desc"
		}
	}
	if strings.Contains(field, ",") {
		segs := strings.SplitN(field, ",", 2)
		field = segs[0]
		if strings.EqualFold(strings.TrimSpace(segs[1]), "desc") {
			dir = "desc"
		}
	}
	return field, dir
}

func normalizeSortDir(dir string) string {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "", "asc", "ascending", "+":
		return "asc"
	case "desc", "descending", "-":
		return "desc"
	default:
		return ""
	}
}
