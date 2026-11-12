package filefilter

// Dynamic programming over path segments and Unicode scalars. It does not use
// filepath.Match (whose classes/escapes differ) or regex translation. Only a
// whole ** segment spans directories; other stars stay inside one segment.
type segment struct {
	deep    bool
	pattern []scalar
}

func prepareSegments(pattern string) ([]segment, error) {
	tokens, err := mapScalars(pattern)
	if err != nil {
		return nil, err
	}
	var out []segment
	for _, s := range splitScalars(tokens) {
		out = append(out, segment{deep: len(s) == 2 && s[0].raw == '*' && s[1].raw == '*', pattern: s})
	}
	return out, nil
}
func matchSegments(pattern []segment, path [][]scalar) bool {
	row := make([]bool, len(path)+1)
	row[0] = true
	for _, p := range pattern {
		next := make([]bool, len(row))
		if p.deep {
			next[0] = row[0]
		}
		for j, s := range path {
			if p.deep {
				next[j+1] = row[j+1] || next[j]
			} else if row[j] {
				next[j+1] = matchSegment(p.pattern, s)
			}
		}
		row = next
	}
	return row[len(path)]
}
func matchSegment(pattern, text []scalar) bool {
	row := make([]bool, len(text)+1)
	row[0] = true
	for _, p := range pattern {
		next := make([]bool, len(row))
		if p.raw == '*' {
			next[0] = row[0]
		}
		for j, c := range text {
			switch p.raw {
			case '*':
				next[j+1] = row[j+1] || next[j]
			case '?':
				next[j+1] = row[j]
			default:
				next[j+1] = row[j] && equal(p, c)
			}
		}
		row = next
	}
	return row[len(text)]
}
