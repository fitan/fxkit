package crudx

import "strings"

// ParseSort parses "col desc", "col,desc", "-col" (desc), "+col" or "col" (asc).
func ParseSort(spec string) (field, dir string) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", ""
	}
	dir = "asc"
	// 支持 doc.go / README 里的 "-createdAt" 前缀写法。
	if strings.HasPrefix(spec, "-") {
		dir = "desc"
		spec = strings.TrimSpace(spec[1:])
	} else if strings.HasPrefix(spec, "+") {
		spec = strings.TrimSpace(spec[1:])
	}
	parts := strings.Fields(spec)
	if len(parts) == 0 {
		return "", ""
	}
	field = parts[0]
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
