package filefilter

import (
	"encoding/json"
	"testing"

	"syncmaid/internal/taskconfig"
)

func TestResidualExtensionTaskDocumentRoundTrip(t *testing.T) {
	const input = `[{"Name":"Synthetic","SourcePath":"C:\\synthetic","Trigger":{"kind":"manual"},"Destinations":[{"Name":"Target","Target":{"kind":"local","Path":"D:\\synthetic"},"Strategy":"AddOnly","Filters":[{"kind":"extension","Extension":"*.*.jpg"}]}]}]`
	ts, e := taskconfig.Decode([]byte(input))
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		r := ts[0].Destinations[0].Filters[0]
		match(t, r, "a.*.jpg", true)
		match(t, r, "a.jpg", false)
		b, e := taskconfig.Encode(ts)
		if e != nil {
			t.Fatal(e)
		}
		var wire []struct {
			Destinations []struct{ Filters []struct{ Extension string } }
		}
		if e = json.Unmarshal(b, &wire); e != nil || wire[0].Destinations[0].Filters[0].Extension != "*.*.jpg" {
			t.Fatal(string(b), e)
		}
		ts, e = taskconfig.Decode(b)
		if e != nil {
			t.Fatal(e)
		}
	}
}
