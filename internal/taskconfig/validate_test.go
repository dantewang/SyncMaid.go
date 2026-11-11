package taskconfig

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestDomainValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func([]Task)
	}{
		{"blank name", func(ts []Task) { ts[0].Name = "  " }},
		{"blank source", func(ts []Task) { ts[0].SourcePath = "" }},
		{"blank target", func(ts []Task) { ts[0].Destinations[0].Target.Path = " " }},
		{"strategy mix", func(ts []Task) { ts[0].Kind = Sync }},
		{"threshold low", func(ts []Task) { ts[0].Destinations[0].MassDeleteThreshold = -0.01 }},
		{"threshold high", func(ts []Task) { ts[0].Destinations[0].MassDeleteThreshold = 1.01 }},
		{"threshold nan", func(ts []Task) { ts[0].Destinations[0].MassDeleteThreshold = math.NaN() }},
		{"task ID duplicate", func(ts []Task) { ts[1].ID = ts[0].ID }},
		{"destination ID duplicate", func(ts []Task) { ts[0].Destinations[1].ID = ts[0].Destinations[0].ID }},
		{"mirror without all", func(ts []Task) {
			ts[0].Kind = Sync
			for i := range ts[0].Destinations {
				ts[0].Destinations[i].Strategy = Mirror
			}
		}},
		{"same source target", func(ts []Task) { ts[0].Destinations[0].Target.Path = ts[0].SourcePath }},
		{"target inside source", func(ts []Task) { ts[0].Destinations[0].Target.Path = `c:/synthetic/inbox/sub` }},
		{"source inside target", func(ts []Task) { ts[0].Destinations[0].Target.Path = `C:\Synthetic` }},
		{"sibling overlap", func(ts []Task) { ts[0].Destinations[1].Target.Path = ts[0].Destinations[0].Target.Path + `\sub` }},
		{"source overlap", func(ts []Task) { ts[1].SourcePath = ts[0].SourcePath + `\sub` }},
		{"destination overlap across tasks", func(ts []Task) {
			d := ts[0].Destinations[0]
			d.ID = NewID()
			d.Strategy = AddOnly
			ts[1].Destinations = []Destination{d}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := legacy(t)
			c.change(ts)
			if _, e := Validate(ts); e == nil {
				t.Fatal("accepted")
			}
		})
	}
	for _, threshold := range []float64{0, 0.5, 1} {
		ts := legacy(t)
		ts[0].Destinations[0].MassDeleteThreshold = threshold
		report, e := Validate(ts)
		if e != nil || len(report.Unverified) == 0 {
			t.Fatal(report, e)
		}
	}
}
func TestLayoutBoundaryAndChaining(t *testing.T) {
	ts := legacy(t)
	ts[1].SourcePath = ts[0].Destinations[0].Target.Path // destination -> another source is legal
	if _, e := Validate(ts); e != nil {
		t.Fatal(e)
	}
	ts[0].Destinations[1].Target.Path = ts[0].Destinations[0].Target.Path + "b"
	if _, e := Validate(ts); e != nil {
		t.Fatal("prefix is not a directory boundary", e)
	}
	for _, s := range []string{`relative\path`, `C:relative`, `\\?\C:\long`, `\\host`, `C:\中文`} {
		ts := legacy(t)
		ts[0].SourcePath = s
		report, e := Validate(ts)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, c := range report.Unverified {
			if c.Field == "tasks[0].SourcePath" && strings.Contains(c.Requirement, "platform resolution") {
				found = true
			}
		}
		if !found {
			t.Fatalf("path falsely resolved: %s", s)
		}
	}
	if !pathsConflict(`\\host\share\dir`, lexicalPath(`\\host\share\dir`), `//HOST/share/dir/sub`, lexicalPath(`//HOST/share/dir/sub`)) {
		t.Fatal("UNC overlap missed")
	}
}
func TestUnsupportedSemanticsSurviveButAreNotValidated(t *testing.T) {
	input := fixture(t, "nested-filters")
	input = []byte(strings.Replace(string(input), "5/15 * * * *", "not a cron", 1))
	input = []byte(strings.Replace(string(input), `(?i)^IMG_\\d+\\.jpg$`, `[`, 1))
	ts, e := Decode(input)
	if e != nil {
		t.Fatal(e)
	}
	r, e := Validate(ts)
	if e != nil {
		t.Fatal("this is a report, not a fake cron/regex parser", e)
	}
	var cron, regex bool
	for _, c := range r.Unverified {
		cron = cron || strings.Contains(c.Requirement, "cron")
		regex = regex || strings.Contains(c.Requirement, "Rust regex")
	}
	if !cron || !regex {
		t.Fatal(r)
	}
	b, e := Encode(ts)
	if e != nil || !strings.Contains(string(b), "not a cron") {
		t.Fatal(e, string(b))
	}
}
func TestInvalidGoVariantsAndUnicode(t *testing.T) {
	bad := legacy(t)
	bad[0].SourcePath = string([]byte{0xff})
	if _, e := Encode(bad); e == nil {
		t.Fatal("invalid UTF8 rewritten")
	}
	ts := legacy(t)
	s := "unused"
	ts[0].Trigger.CronExpression = &s
	if _, e := Encode(ts); e == nil {
		t.Fatal("incompatible trigger accepted")
	}
	ts = legacy(t)
	ts[0].Destinations[0].Filters[0].Pattern = &s
	if _, e := Encode(ts); e == nil {
		t.Fatal("incompatible filter accepted")
	}
	for _, value := range []string{`"\ud800"`, `"\udfff"`, `"\ud800\u0041"`} {
		var target Target
		if e := json.Unmarshal([]byte(`{"kind":"local","Path":`+value+`}`), &target); e == nil {
			t.Fatal(value)
		}
	}
	var target Target
	if e := json.Unmarshal([]byte(`{"kind":"local","Path":"D:\\emoji\ud83d\ude00"}`), &target); e != nil || target.Path != "D:\\emoji😀" {
		t.Fatal(target, e)
	}
}

func TestAlternateIDSpellingsStillConflict(t *testing.T) {
	ts := legacy(t)
	ts[0].ID = "abcdef00-1234-5678-9abc-def012345678"
	ts[1].ID = "ABCDEF00123456789ABCDEF012345678"
	if _, e := Validate(ts); e == nil {
		t.Fatal("alternate UUID spelling evaded duplicate check")
	}
}
