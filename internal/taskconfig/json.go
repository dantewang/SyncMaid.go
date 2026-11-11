package taskconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ErrUnsupported distinguishes a closed enum/variant from malformed JSON.
// Callers must preserve the input instead of loading defaults over it.
var ErrUnsupported = errors.New("unsupported task configuration type")

// Decode is all-or-error: malformed/unsupported input never becomes an empty list.
// Unknown members survive round trips, including nested variant members. Unknown
// discriminators cannot safely be interpreted and are rejected.
func Decode(data []byte) ([]Task, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("tasks: invalid UTF-8")
	}
	if b := bytes.TrimSpace(data); len(b) == 0 || b[0] != '[' {
		return nil, fmt.Errorf("tasks: expected array")
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("tasks: %w", err)
	}
	tasks := make([]Task, len(raw))
	for i, r := range raw {
		if err := json.Unmarshal(r, &tasks[i]); err != nil {
			return nil, fmt.Errorf("tasks[%d]: %w", i, err)
		}
	}
	return tasks, nil
}

// Encode checks shape, not runtime readiness. Nil is deliberately not treated as
// an empty configuration. No filesystem writes or backup decisions happen here.
func Encode(tasks []Task) ([]byte, error) {
	if tasks == nil {
		return nil, fmt.Errorf("tasks: nil is not an explicit empty list")
	}
	b, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return nil, err
	}
	if _, err = Decode(b); err != nil {
		return nil, err
	}
	return b, nil
}

// object uses exact case and rejects duplicates (encoding/json alone folds case
// and accepts duplicate keys). Unknown fields remain opaque instead of discarded.
func object(data []byte) (extras, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("invalid UTF-8")
	}
	if err := checkSurrogates(data); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	if t != json.Delim('{') {
		return nil, fmt.Errorf("expected object")
	}
	o := extras{}
	for d.More() {
		t, err = d.Token()
		if err != nil {
			return nil, err
		}
		k := t.(string)
		if _, exists := o[k]; exists {
			return nil, fmt.Errorf("duplicate member %q", k)
		}
		var v json.RawMessage
		if err = d.Decode(&v); err != nil {
			return nil, err
		}
		o[k] = v
	}
	if _, err = d.Token(); err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	return o, nil
}

// encoding/json replaces unpaired UTF-16 escapes with U+FFFD; serde rejects
// them. Reject before decoding rather than silently changing a stored path.
func checkSurrogates(data []byte) error {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) || data[i] != 'u' || i+4 >= len(data) {
			continue
		}
		n, e := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if e != nil {
			continue
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return fmt.Errorf("unpaired Unicode surrogate")
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return fmt.Errorf("unpaired Unicode surrogate")
			}
			low, e := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return fmt.Errorf("unpaired Unicode surrogate")
			}
			i += 6
		}
	}
	return nil
}
func readObject(data []byte, into any, allowed, required string) (extras, error) {
	o, err := object(data)
	if err != nil {
		return nil, err
	}
	for _, k := range strings.Fields(required) {
		if _, ok := o[k]; !ok {
			return nil, fmt.Errorf("missing %s", k)
		}
	}
	known := extras{}
	for _, k := range strings.Fields(allowed) {
		if v, ok := o[k]; ok {
			if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				return nil, fmt.Errorf("%s: null is not allowed", k)
			}
			known[k] = v
			delete(o, k)
		}
	}
	b, _ := json.Marshal(known)
	if err = json.Unmarshal(b, into); err != nil {
		return nil, err
	}
	return o, nil
}
func writeObject(v any, extra extras) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	o, err := object(b)
	if err != nil {
		return nil, err
	}
	for k, v := range extra {
		if _, exists := o[k]; exists {
			return nil, fmt.Errorf("extension conflicts with %s", k)
		}
		o[k] = v
	}
	return json.Marshal(o)
}
func memberKind(data []byte) (string, error) {
	o, err := object(data)
	if err != nil {
		return "", err
	}
	var k string
	v, ok := o["kind"]
	if !ok || string(v) == "null" {
		return "", fmt.Errorf("missing kind")
	}
	if err = json.Unmarshal(v, &k); err != nil {
		return "", err
	}
	return k, nil
}
func validEnum[T ~string](v T, values ...T) error {
	for _, s := range values {
		if v == s {
			return nil
		}
	}
	return fmt.Errorf("%w: value %q", ErrUnsupported, v)
}
func (t *Task) UnmarshalJSON(data []byte) error {
	type wire Task
	w := wire{ID: NewID()}
	// Option<Kind> accepts null as well as absence, unlike other defaulted fields.
	o, err := object(data)
	if err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(o["Kind"]), []byte("null")) {
		delete(o, "Kind")
		data, _ = json.Marshal(o)
	}
	w.extra, err = readObject(data, &w, "Name SourcePath Trigger Destinations Id Kind", "Name SourcePath Trigger Destinations")
	if err != nil {
		return err
	}
	w.ID, err = parseID(string(w.ID))
	if err != nil {
		return fmt.Errorf("Id: %w", err)
	}
	if w.Kind == "" { // Only absence derives; an explicit empty string is an unknown enum.
		if _, exists := o["Kind"]; exists {
			return fmt.Errorf("Kind: %w: empty value", ErrUnsupported)
		}
		w.Kind = Sync
		for _, d := range w.Destinations {
			if d.Strategy == Move {
				w.Kind = Moving
				break
			}
		}
	}
	if err = validEnum(w.Kind, Sync, Moving); err != nil {
		return fmt.Errorf("Kind: %w", err)
	}
	*t = Task(w)
	return nil
}
func validText(values ...string) error {
	for _, v := range values {
		if !utf8.ValidString(v) {
			return fmt.Errorf("invalid UTF-8 string")
		}
	}
	return nil
}
func (t Task) MarshalJSON() ([]byte, error) {
	if err := validText(t.Name, t.SourcePath); err != nil {
		return nil, err
	}
	type wire Task
	return writeObject(wire(t), t.extra)
}
func (d *Destination) UnmarshalJSON(data []byte) error {
	type wire Destination
	w := wire{ID: NewID(), DeleteMode: Recycle, CollisionPolicy: Skip, MassDeleteThreshold: 0.5}
	var err error
	w.extra, err = readObject(data, &w, "Name Target Filters Strategy Id VerifyContents DeleteMode FlattenStructure CollisionPolicy MassDeleteThreshold", "Name Target Filters Strategy")
	if err != nil {
		return err
	}
	w.ID, err = parseID(string(w.ID))
	if err != nil {
		return fmt.Errorf("Id: %w", err)
	}
	if err = validEnum(w.Strategy, Mirror, AddOnly, Move); err != nil {
		return fmt.Errorf("Strategy: %w", err)
	}
	if err = validEnum(w.DeleteMode, Recycle, Permanent); err != nil {
		return fmt.Errorf("DeleteMode: %w", err)
	}
	if err = validEnum(w.CollisionPolicy, Skip, Suffix); err != nil {
		return fmt.Errorf("CollisionPolicy: %w", err)
	}
	*d = Destination(w)
	return nil
}
func (d Destination) MarshalJSON() ([]byte, error) {
	if err := validText(d.Name); err != nil {
		return nil, err
	}
	type wire Destination
	return writeObject(wire(d), d.extra)
}
func (t *Target) UnmarshalJSON(data []byte) error {
	type wire Target
	var w wire
	k, err := memberKind(data)
	if err != nil {
		return err
	}
	if k != "local" {
		return fmt.Errorf("Target: %w: kind %q", ErrUnsupported, k)
	}
	w.extra, err = readObject(data, &w, "kind Path", "kind Path")
	if err != nil {
		return err
	}
	*t = Target(w)
	return nil
}
func (t Target) MarshalJSON() ([]byte, error) {
	if err := validText(t.Path); err != nil {
		return nil, err
	}
	type wire Target
	return writeObject(wire(t), t.extra)
}
func (t *Trigger) UnmarshalJSON(data []byte) error {
	type wire Trigger
	var w wire
	k, err := memberKind(data)
	if err != nil {
		return err
	}
	fields := "kind"
	switch k {
	case "manual":
	case "scheduled":
		fields += " CronExpression"
	case "watch":
		n := int32(10)
		w.SettleSeconds = &n
		fields += " SettleSeconds"
	default:
		return fmt.Errorf("Trigger: %w: kind %q", ErrUnsupported, k)
	}
	required := fields
	if k == "watch" {
		required = "kind"
	}
	w.extra, err = readObject(data, &w, fields, required)
	if err != nil {
		return err
	}
	*t = Trigger(w)
	return nil
}
func (t Trigger) MarshalJSON() ([]byte, error) {
	if t.Kind == "watch" && t.SettleSeconds == nil {
		return nil, fmt.Errorf("Trigger: watch requires an explicit settle period in memory")
	}
	if t.CronExpression != nil {
		if err := validText(*t.CronExpression); err != nil {
			return nil, err
		}
	}
	if (t.Kind != "scheduled" && t.CronExpression != nil) || (t.Kind != "watch" && t.SettleSeconds != nil) {
		return nil, fmt.Errorf("Trigger: incompatible payload")
	}
	type wire Trigger
	return writeObject(wire(t), t.extra)
}
func (r *Rule) UnmarshalJSON(data []byte) error {
	type wire Rule
	var w wire
	k, err := memberKind(data)
	if err != nil {
		return err
	}
	fields := "kind"
	switch k {
	case "all":
	case "path":
		fields += " Prefix"
	case "extension":
		fields += " Extension"
	case "wildcard", "regex":
		fields += " Pattern"
	case "allOf", "anyOf":
		fields += " Rules"
	case "not":
		fields += " Rule"
	default:
		return fmt.Errorf("Filter: %w: kind %q", ErrUnsupported, k)
	}
	w.extra, err = readObject(data, &w, fields, fields)
	if err != nil {
		return err
	}
	if k == "path" {
		s := strings.Trim(strings.ReplaceAll(*w.Prefix, `\`, "/"), "/")
		w.Prefix = &s
	}
	if k == "extension" {
		s := strings.TrimLeft(strings.TrimLeft(*w.Extension, "*"), ".")
		w.Extension = &s
	}
	if k == "wildcard" {
		s := strings.Trim(strings.ReplaceAll(*w.Pattern, `\`, "/"), "/")
		w.Pattern = &s
	}
	*r = Rule(w)
	return nil
}
func (r Rule) MarshalJSON() ([]byte, error) {
	for _, s := range []*string{r.Prefix, r.Extension, r.Pattern} {
		if s != nil {
			if err := validText(*s); err != nil {
				return nil, err
			}
		}
	}
	if (r.Kind != "path" && r.Prefix != nil) || (r.Kind != "extension" && r.Extension != nil) || (r.Kind != "regex" && r.Kind != "wildcard" && r.Pattern != nil) || (r.Kind != "allOf" && r.Kind != "anyOf" && r.Rules != nil) || (r.Kind != "not" && r.Rule != nil) {
		return nil, fmt.Errorf("Filter: incompatible payload")
	}
	type wire Rule
	return writeObject(wire(r), r.extra)
}
