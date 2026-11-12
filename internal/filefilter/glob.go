package filefilter

// Dynamic programming over path segments and Unicode scalars. It does not use
// filepath.Match (whose classes/escapes differ) or regex translation. Only a
// whole ** segment spans directories; other stars stay inside one segment.
type segment struct {
	deep    bool
	pattern []rune
}

func prepareSegments(pattern string) []segment {
	var out []segment
	for _, s := range split(pattern) {
		out = append(out, segment{deep: s == "**", pattern: []rune(s)})
	}
	return out
}
func matchSegments(pattern []segment, path []string) bool {
	input := make([][]rune, len(path))
	for i, s := range path {
		input[i] = []rune(s)
	}
	row := make([]bool, len(path)+1)
	row[0] = true
	for _, p := range pattern {
		next := make([]bool, len(row))
		if p.deep {
			next[0] = row[0]
		}
		for j, s := range input {
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
func matchSegment(pattern, text []rune) bool {
	row := make([]bool, len(text)+1)
	row[0] = true
	for _, p := range pattern {
		next := make([]bool, len(row))
		if p == '*' {
			next[0] = row[0]
		}
		for j, c := range text {
			switch p {
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
