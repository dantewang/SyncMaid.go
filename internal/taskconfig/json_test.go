package taskconfig

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Fixtures are invented, not copied user data. Shapes derive from Rust baseline
// 1750c7e core/model/{task,destination,mod}.rs, filtering.rs, triggers/mod.rs
// and persistence/stores.rs model/legacy round-trip tests.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name + ".json")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func legacy(t *testing.T) []Task {
	t.Helper()
	ts, e := Decode(fixture(t, "legacy-no-id-kind"))
	if e != nil {
		t.Fatal(e)
	}
	return ts
}
func TestLegacyDefaultsAndRoundTrip(t *testing.T) {
	ts := legacy(t)
	if ts[0].Kind != Moving || ts[1].Kind != Sync || ts[0].Destinations[0].Filters[0].Extension == nil || *ts[0].Destinations[0].Filters[0].Extension != "jpg" {
		t.Fatal(ts)
	}
	seen := map[ID]bool{}
	for _, task := range ts {
		for _, id := range append([]ID{task.ID}, func() []ID {
			var ids []ID
			for _, d := range task.Destinations {
				ids = append(ids, d.ID)
			}
			return ids
		}()...) {
			if seen[id] || len(id) != 36 || id[14] != '4' {
				t.Fatalf("bad generated UUID %q", id)
			}
			seen[id] = true
		}
	}
	d := ts[0].Destinations[0]
	if d.VerifyContents || d.FlattenStructure || d.DeleteMode != Recycle || d.CollisionPolicy != Skip || d.MassDeleteThreshold != 0.5 {
		t.Fatal(d)
	}
	if got, ok := ts[1].Trigger.SettleWindow(); !ok || got != 10*time.Second {
		t.Fatal(got, ok)
	}
	b, e := Encode(ts)
	if e != nil {
		t.Fatal(e)
	}
	again, e := Decode(b)
	if e != nil || !reflect.DeepEqual(ts, again) {
		t.Fatal(e, string(b))
	}
	if again[0].Destinations[0].Name != "First" || again[0].Destinations[1].Name != "Remainder" {
		t.Fatal("order changed")
	}
	// Editing names leaves persisted identity untouched.
	id := ts[0].ID
	ts[0].Name = "Renamed"
	b, e = Encode(ts)
	if e != nil {
		t.Fatal(e)
	}
	again, e = Decode(b)
	if e != nil || again[0].ID != id {
		t.Fatal(e)
	}
}
func TestNestedExtensionsAndPathsAreNotLost(t *testing.T) {
	ts, e := Decode(fixture(t, "nested-filters"))
	if e != nil {
		t.Fatal(e)
	}
	b, e := Encode(ts)
	if e != nil {
		t.Fatal(e)
	}
	next, e := Decode(b)
	if e != nil || !reflect.DeepEqual(ts, next) {
		t.Fatal(e, string(b))
	}
	for _, value := range []string{"FutureTask", "FutureDestination", "FutureTarget", "FutureTrigger", "FutureRule", "9007199254740993"} {
		if !strings.Contains(string(b), value) {
			t.Fatalf("lost %s", value)
		}
	}
	if ts[0].SourcePath != `C:\Synthetic\Photos` || ts[0].Destinations[0].Target.Path != `\\host\share\合成` {
		t.Fatal("path was rewritten")
	}
	report, e := Validate(ts)
	if e != nil {
		t.Fatal(e)
	}
	var requirements string
	for _, c := range report.Unverified {
		requirements += c.Requirement + "\n"
	}
	for _, want := range []string{"cron", "Rust regex", "unknown-field", "filesystem"} {
		if !strings.Contains(requirements, want) {
			t.Fatal(requirements)
		}
	}
}
func TestDecodeRejectsWithoutPartialTasks(t *testing.T) {
	base := `{"Name":"T","SourcePath":"C:\\src","Trigger":{"kind":"manual"},"Destinations":[]}`
	cases := map[string]string{
		"null root": "null", "object root": "{}", "null element": "[null]", "trailing": "[] []",
		"missing source":  "[{\"Name\":\"T\",\"Trigger\":{\"kind\":\"manual\"},\"Destinations\":[]}]",
		"wrong case":      "[" + strings.Replace(base, "SourcePath", "sourcePath", 1) + "]",
		"null required":   "[" + strings.Replace(base, `"Destinations":[]`, `"Destinations":null`, 1) + "]",
		"unknown kind":    "[" + strings.Replace(base, `"Destinations":[]`, `"Destinations":[],"Kind":"Copy"`, 1) + "]",
		"empty kind":      "[" + strings.Replace(base, `"Destinations":[]`, `"Destinations":[],"Kind":""`, 1) + "]",
		"null ID":         "[" + strings.Replace(base, `"Destinations":[]`, `"Destinations":[],"Id":null`, 1) + "]",
		"bad ID":          "[" + strings.Replace(base, `"Destinations":[]`, `"Destinations":[],"Id":"not-a-uuid"`, 1) + "]",
		"duplicate key":   "[" + strings.Replace(base, `"Name":"T"`, `"Name":"T","Name":"Other"`, 1) + "]",
		"unknown trigger": "[" + strings.Replace(base, "manual", "watching", 1) + "]",
		"missing cron":    "[" + strings.Replace(base, "manual", "scheduled", 1) + "]",
		"partial list":    "[" + base + ",{}]",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			got, e := Decode([]byte(input))
			if e == nil || got != nil {
				t.Fatalf("got %v %v", got, e)
			}
		})
	}
	if _, e := Decode([]byte{'[', '"', 0xff, '"', ']'}); e == nil {
		t.Fatal("invalid utf8 accepted")
	}
	if _, e := Encode(nil); e == nil {
		t.Fatal("nil saved as empty")
	}
	ts, e := Decode([]byte("[]"))
	if e != nil || ts == nil {
		t.Fatal(e)
	}
	if _, e = Encode(ts); e != nil {
		t.Fatal(e)
	}
}
func TestDestinationWireBoundaries(t *testing.T) {
	base := `{"Name":"D","Target":{"kind":"local","Path":"D:\\dst"},"Filters":[{"kind":"all"}],"Strategy":"Mirror"}`
	cases := map[string]string{
		"missing filters": strings.Replace(base, `"Filters":[{"kind":"all"}],`, "", 1),
		"null bool":       strings.TrimSuffix(base, "}") + `,"VerifyContents":null}`,
		"null threshold":  strings.TrimSuffix(base, "}") + `,"MassDeleteThreshold":null}`,
		"bad strategy":    strings.Replace(base, "Mirror", "Unknown", 1),
		"bad delete":      strings.TrimSuffix(base, "}") + `,"DeleteMode":"Trash"}`,
		"bad collision":   strings.TrimSuffix(base, "}") + `,"CollisionPolicy":"Overwrite"}`,
		"unknown target":  strings.Replace(base, "local", "cloud", 1),
		"null path":       strings.Replace(base, `"D:\\dst"`, "null", 1),
		"unknown filter":  strings.Replace(base, "all", "future", 1),
		"missing payload": strings.Replace(base, "all", "not", 1),
		"null rules":      strings.Replace(base, `{"kind":"all"}`, `{"kind":"allOf","Rules":null}`, 1),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			var d Destination
			if e := json.Unmarshal([]byte(input), &d); e == nil {
				t.Fatal("accepted", input)
			}
		})
	}
}
func TestWatchRangeKindNullAndUUIDForms(t *testing.T) {
	for _, c := range []struct {
		n    int64
		want time.Duration
	}{{-2147483648, time.Second}, {0, time.Second}, {1, time.Second}, {600, 600 * time.Second}, {2147483647, 600 * time.Second}} {
		b, _ := json.Marshal(map[string]any{"kind": "watch", "SettleSeconds": c.n})
		var tr Trigger
		if e := json.Unmarshal(b, &tr); e != nil {
			t.Fatal(e)
		}
		if got, _ := tr.SettleWindow(); got != c.want || int64(*tr.SettleSeconds) != c.n {
			t.Fatal(got, tr)
		}
	}
	for _, s := range []string{`{"kind":"watch","SettleSeconds":2147483648}`, `{"kind":"watch","SettleSeconds":null}`, `{"kind":"watch","SettleSeconds":1.5}`} {
		var tr Trigger
		if json.Unmarshal([]byte(s), &tr) == nil {
			t.Fatal(s)
		}
	}
	for _, id := range []string{"ABCDEF00123456789ABCDEF012345678", "{abcdef00-1234-5678-9abc-def012345678}", "urn:uuid:abcdef00-1234-5678-9abc-def012345678"} {
		if got, e := parseID(id); e != nil || got != "abcdef00-1234-5678-9abc-def012345678" {
			t.Fatal(got, e)
		}
	}
	var task Task
	if e := json.Unmarshal([]byte(`{"Name":"T","SourcePath":"C:\\s","Trigger":{"kind":"manual"},"Destinations":[],"Kind":null,"Id":"00000000-0000-0000-0000-000000000000"}`), &task); e != nil || task.Kind != Sync {
		t.Fatal(task, e)
	}
}

func TestCaseDistinctAndVariantExtensionsRemainOpaque(t *testing.T) {
	input := `[{"Name":"Real","name":"shadow","SourcePath":"C:\\raw\\..\\source","Trigger":{"kind":"manual","CronExpression":"future"},"Destinations":[]}]`
	ts, e := Decode([]byte(input))
	if e != nil {
		t.Fatal(e)
	}
	if ts[0].Name != "Real" || ts[0].SourcePath != `C:\raw\..\source` || ts[0].Trigger.CronExpression != nil {
		t.Fatal(ts)
	}
	b, e := Encode(ts)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), `"name": "shadow"`) || !strings.Contains(string(b), `"CronExpression": "future"`) {
		t.Fatal(string(b))
	}
	r, e := Validate(ts)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, c := range r.Unverified {
		found = found || strings.Contains(c.Requirement, "unknown-field")
	}
	if !found {
		t.Fatal(r)
	}
	if _, e = json.Marshal(Trigger{Kind: "watch"}); e == nil {
		t.Fatal("unset in-memory watch silently defaulted")
	}
}
