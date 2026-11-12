package taskconfig

import (
	"encoding/json"
	"testing"
)

func TestResidualExtensionWireSurvivesEditsAndRoundTrips(t *testing.T) {
	const original = `{"kind":"extension","Extension":"***...*..jpg"}`
	var r Rule
	if e := json.Unmarshal([]byte(original), &r); e != nil {
		t.Fatal(e)
	}
	if *r.Extension != "*..jpg" {
		t.Fatal(*r.Extension)
	}
	for i := 0; i < 3; i++ {
		b, e := json.Marshal(r)
		if e != nil {
			t.Fatal(e)
		}
		var wire struct{ Extension string }
		if e = json.Unmarshal(b, &wire); e != nil || wire.Extension != "***...*..jpg" {
			t.Fatal("wire extension spelling changed", string(b), e)
		}
		if e = json.Unmarshal(b, &r); e != nil {
			t.Fatal(e)
		}
		if *r.Extension != "*..jpg" {
			t.Fatal("normalized again", *r.Extension)
		}
	}
	*r.Extension = "png"
	b, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	var edited Rule
	if e = json.Unmarshal(b, &edited); e != nil || *edited.Extension != "png" {
		t.Fatal(string(b), e)
	}
	canonical := "*.jpg"
	direct := Rule{Kind: "extension", Extension: &canonical}
	b, e = json.Marshal(direct)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &edited); e != nil || *edited.Extension != canonical {
		t.Fatal(string(b), e)
	}
}
