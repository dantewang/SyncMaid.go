package filefilter

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

const unicodeVersion = "17.0.0"

func checkUnicodeVersions(goVersion, textVersion string) error {
	if goVersion != unicodeVersion || textVersion != unicodeVersion {
		return fmt.Errorf("%w: Go=%s x/text=%s expected=%s", ErrUnicodeVersion, goVersion, textVersion, unicodeVersion)
	}
	return nil
}
func checkUnicodeTables() error { return checkUnicodeVersions(unicode.Version, cases.UnicodeVersion) }

// A key is a full uppercase sequence of one ORIGINAL scalar. Unicode17 expands
// to at most three scalars (independently checked against UCD reference data).
// Keys are comparable without flattening original scalar positions or allocating
// a mapped string for every comparison. No normalization or simple folding.
type upperKey struct {
	runes [3]rune
	n     uint8
}
type scalar struct {
	raw   rune
	upper upperKey
}

type scalarMapper struct {
	caser cases.Caser
	src   [utf8.UTFMax]byte
	dst   [3 * utf8.UTFMax]byte
}

func newScalarMapper() scalarMapper { return scalarMapper{caser: cases.Upper(language.Und)} }

func (mapper *scalarMapper) uppercase(r rune) (upperKey, error) {
	if r < utf8.RuneSelf {
		u := r
		if u >= 'a' && u <= 'z' {
			u -= 'a' - 'A'
		}
		return upperKey{runes: [3]rune{u}, n: 1}, nil
	}
	size := utf8.EncodeRune(mapper.src[:], r)
	mapper.caser.Reset()
	written, consumed, err := mapper.caser.Transform(mapper.dst[:], mapper.src[:size], true)
	if err != nil || consumed != size || !utf8.Valid(mapper.dst[:written]) {
		return upperKey{}, fmt.Errorf("%w: scalar U+%04X consumed=%d/%d: %v", ErrMapping, r, consumed, size, err)
	}
	var key upperKey
	for _, u := range string(mapper.dst[:written]) {
		if key.n == 3 {
			return upperKey{}, fmt.Errorf("%w: scalar U+%04X exceeds key capacity", ErrMapping, r)
		}
		key.runes[key.n] = u
		key.n++
	}
	if key.n == 0 {
		return upperKey{}, fmt.Errorf("%w: empty mapping for U+%04X", ErrMapping, r)
	}
	return key, nil
}

// Each preparation/path owns its Caser, reusable byte buffers and output. No
// global mutable Caser or retained cache; Transform runs once per original scalar.
func mapScalars(s string) ([]scalar, error) {
	mapper := newScalarMapper()
	out := make([]scalar, 0, utf8.RuneCountInString(s))
	for _, r := range s {
		key, err := mapper.uppercase(r)
		if err != nil {
			return nil, err
		}
		out = append(out, scalar{raw: r, upper: key})
	}
	return out, nil
}
func equal(a, b scalar) bool { return a.raw == b.raw || a.upper == b.upper }
func separator(r rune) bool  { return r == '/' || r == '\\' }

type pathInput struct {
	scalars  []scalar
	segments [][]scalar
}

func preparePath(path string) (pathInput, error) {
	tokens, err := mapScalars(path)
	if err != nil {
		return pathInput{}, err
	}
	return pathInput{scalars: tokens, segments: splitScalars(tokens)}, nil
}
func splitScalars(tokens []scalar) [][]scalar {
	var segments [][]scalar
	start := 0
	for i, s := range tokens {
		if separator(s.raw) {
			if start < i {
				segments = append(segments, tokens[start:i])
			}
			start = i + 1
		}
	}
	if start < len(tokens) {
		segments = append(segments, tokens[start:])
	}
	return segments
}
