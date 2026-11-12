package filefilter

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"testing"
	"unicode/utf8"

	"syncmaid/internal/taskconfig"
)

// Golden is NOT generated from x/text. Reference: Unicode17 UnicodeData index12
// plus unconditional SpecialCasing index3 (zero-based fields), else identity,
// excluding surrogates. Official files:
// https://www.unicode.org/Public/17.0.0/ucd/UnicodeData.txt
// https://www.unicode.org/Public/17.0.0/ucd/SpecialCasing.txt
// SHA256 respectively:
// 2e1efc1dcb59c575eedf5ccae60f95229f706ee6d031835247d843c11d96470c
// efc25faf19de21b92c1194c111c932e03d2a5eaf18194e33f1156e96de4c9588
// Stream per scalar ascending: input uint32 big-endian, output scalar count
// uint8, then output scalars uint32 big-endian. A digest keeps the test offline
// without vendoring a giant generated Unicode table or using the same library
// to derive expected output. Rust1.97.1 generator uses these same UCD rules.
func TestUnicode17ExhaustiveReferenceDigest(t *testing.T) {
	if e := checkUnicodeTables(); e != nil {
		t.Fatal(e)
	}
	h := sha256.New()
	mapper := newScalarMapper()
	count := 0
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue
		}
		key, e := mapper.uppercase(r)
		if e != nil {
			t.Fatal(e)
		}
		var buf [17]byte
		binary.BigEndian.PutUint32(buf[:4], uint32(r))
		buf[4] = key.n
		for i := uint8(0); i < key.n; i++ {
			binary.BigEndian.PutUint32(buf[5+int(i)*4:9+int(i)*4], uint32(key.runes[i]))
		}
		h.Write(buf[:5+int(key.n)*4])
		count++
	}
	got := fmt.Sprintf("%x", h.Sum(nil))
	const want = "3e3258dd6e5dec1333f4debebd81b017f9f4dbcb54cf5fae4a3ce691ba39bdd9"
	if count != 1112064 || got != want {
		t.Fatalf("Unicode17 scalar count=%d digest=%s want=%s", count, got, want)
	}
	t.Logf("independent UCD17 reference: %d scalars, digest=%s, differences=0", count, got)
}
func TestUnicodeVersionGuard(t *testing.T) {
	for _, pair := range [][2]string{{"16.0.0", "17.0.0"}, {"17.0.0", "15.0.0"}, {"18.0.0", "18.0.0"}} {
		if e := checkUnicodeVersions(pair[0], pair[1]); !errors.Is(e, ErrUnicodeVersion) {
			t.Fatal(e)
		}
	}
}
func TestFullUnicodeOriginalScalarAlignment(t *testing.T) {
	for _, c := range []struct {
		pattern, path string
		want          bool
	}{
		{"ß", "ß", true}, {"ß", "ẞ", false}, {"ß", "SS", false}, {"ﬅ", "ﬆ", true}, {"µ", "Μ", true}, {"ſ", "S", true}, {"ÿ", "Ÿ", true}, {"σ", "ς", true}, {"ΐ", "Ι\u0308\u0301", false}, {"i", "İ", false}, {"ı", "I", true}, {"K", "k", false}, {"Ω", "Ω", false}, {"é", "e\u0301", false}, {"😀", "😀", true}, {"\U00010428", "\U00010400", true},
	} {
		match(t, taskconfig.PathFilter(c.pattern), c.path, c.want)
	}
	for _, c := range []struct {
		pattern, path string
		want          bool
	}{
		{"?.txt", "ß.txt", true}, {"?.txt", "ﬅ.txt", true}, {"?.txt", "SS.txt", false}, {"?.txt", "😀.txt", true}, {"?.txt", "e\u0301.txt", false}, {"??.txt", "e\u0301.txt", true}, {"ﬅ*.txt", "ﬆmore.txt", true}, {"ß*.txt", "SSmore.txt", false}, {"**/σ?.TXT", "папка/ς😀.txt", true},
	} {
		match(t, taskconfig.WildcardFilter(c.pattern), c.path, c.want)
	}
}
func TestMatcherConcurrentUnicodeCalls(t *testing.T) {
	m, e := Compile([]taskconfig.Rule{taskconfig.WildcardFilter("**/ﬅ?.TXT")})
	if e != nil {
		t.Fatal(e)
	}
	const workers = 16
	fail := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 64; j++ {
				got, e := m.Match("папка/ﬆ😀.txt")
				if e != nil || !got {
					fail <- fmt.Errorf("match=%v error=%v", got, e)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(fail)
	for e := range fail {
		t.Error(e)
	}
}
func TestDepthBoundary128And129(t *testing.T) {
	for _, depth := range []int{128, 129} {
		r := taskconfig.Rule{Kind: "all"}
		for i := 0; i < depth; i++ {
			r = not(r)
		}
		m, e := Compile([]taskconfig.Rule{r})
		if depth == 128 {
			if e != nil {
				t.Fatal(e)
			}
			got, e := m.Match("a")
			if e != nil || !got {
				t.Fatal(got, e)
			}
		} else if m != nil || !errors.Is(e, ErrInvalidRule) {
			t.Fatal(m, e)
		}
	}
}
