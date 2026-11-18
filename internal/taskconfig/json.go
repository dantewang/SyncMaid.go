package taskconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
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
	return json.MarshalIndent(tasks, "", "  ")
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
		var compact bytes.Buffer
		if err = json.Compact(&compact, v); err != nil {
			return nil, err
		}
		o[k] = compact.Bytes()
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
type jsonFieldSpec struct {
	name      string
	index     int
	omitEmpty bool
}

func structJSONFields(t reflect.Type) ([]jsonFieldSpec, map[string]int) {
	fields := make([]jsonFieldSpec, 0, t.NumField())
	byName := make(map[string]int, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, rest, _ := strings.Cut(tag, ",")
		if name == "" {
			name = sf.Name
		}
		fields = append(fields, jsonFieldSpec{
			name:      name,
			index:     i,
			omitEmpty: rest == "omitempty" || strings.Contains(","+rest+",", ",omitempty,"),
		})
		byName[name] = i
	}
	return fields, byName
}

func readObject(data []byte, into any, allowed, required string) (extras, error) {
	o, err := object(data)
	if err != nil {
		return nil, err
	}
	return readParsedObject(o, into, allowed, required)
}

func readParsedObject(o extras, into any, allowed, required string) (extras, error) {
	for _, k := range strings.Fields(required) {
		if _, ok := o[k]; !ok {
			return nil, fmt.Errorf("missing %s", k)
		}
	}
	rv := reflect.ValueOf(into).Elem()
	_, byName := structJSONFields(rv.Type())
	for _, k := range strings.Fields(allowed) {
		v, ok := o[k]
		if !ok {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, fmt.Errorf("%s: null is not allowed", k)
		}
		idx, ok := byName[k]
		if !ok {
			return nil, fmt.Errorf("unknown struct field %s", k)
		}
		if err := json.Unmarshal(v, rv.Field(idx).Addr().Interface()); err != nil {
			return nil, err
		}
		delete(o, k)
	}
	return o, nil
}

func writeObject(v any, extra extras) ([]byte, error) {
	rv := reflect.ValueOf(v)
	fields, _ := structJSONFields(rv.Type())
	o := make(extras, len(fields)+len(extra))
	for _, f := range fields {
		fv := rv.Field(f.index)
		if f.omitEmpty && fv.IsZero() {
			continue
		}
		b, err := json.Marshal(fv.Interface())
		if err != nil {
			return nil, err
		}
		o[f.name] = b
	}
	for k, val := range extra {
		if _, exists := o[k]; exists {
			return nil, fmt.Errorf("extension conflicts with %s", k)
		}
		o[k] = val
	}
	return json.Marshal(o)
}

func parseObjectKind(data []byte) (extras, string, error) {
	o, err := object(data)
	if err != nil {
		return nil, "", err
	}
	var k string
	v, ok := o["kind"]
	if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		return nil, "", fmt.Errorf("missing kind")
	}
	if err = json.Unmarshal(v, &k); err != nil {
		return nil, "", err
	}
	return o, k, nil
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
	kindPresent := false
	if rawKind, exists := o["Kind"]; exists {
		if bytes.Equal(bytes.TrimSpace(rawKind), []byte("null")) {
			delete(o, "Kind")
		} else {
			kindPresent = true
		}
	}
	w.extra, err = readParsedObject(o, &w, "Name SourcePath Trigger Destinations Id Kind", "Name SourcePath Trigger Destinations")
	if err != nil {
		return err
	}
	w.ID, err = parseID(string(w.ID))
	if err != nil {
		return fmt.Errorf("Id: %w", err)
	}
	if w.Kind == "" { // Only absence derives; an explicit empty string is an unknown enum.
		if kindPresent {
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
	id, err := parseID(string(t.ID))
	if err != nil {
		return nil, fmt.Errorf("Id: %w", err)
	}
	if err := validEnum(t.Kind, Sync, Moving); err != nil {
		return nil, fmt.Errorf("Kind: %w", err)
	}
	if t.Destinations == nil {
		return nil, fmt.Errorf("Destinations: null is not allowed")
	}
	type wire Task
	w := wire(t)
	w.ID = id
	return writeObject(w, t.extra)
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
	id, err := parseID(string(d.ID))
	if err != nil {
		return nil, fmt.Errorf("Id: %w", err)
	}
	if d.Filters == nil {
		return nil, fmt.Errorf("Filters: null is not allowed")
	}
	if err := validEnum(d.Strategy, Mirror, AddOnly, Move); err != nil {
		return nil, fmt.Errorf("Strategy: %w", err)
	}
	if err := validEnum(d.DeleteMode, Recycle, Permanent); err != nil {
		return nil, fmt.Errorf("DeleteMode: %w", err)
	}
	if err := validEnum(d.CollisionPolicy, Skip, Suffix); err != nil {
		return nil, fmt.Errorf("CollisionPolicy: %w", err)
	}
	type wire Destination
	w := wire(d)
	w.ID = id
	return writeObject(w, d.extra)
}
func (t *Target) UnmarshalJSON(data []byte) error {
	type wire Target
	var w wire
	o, k, err := parseObjectKind(data)
	if err != nil {
		return err
	}
	if k != "local" {
		return fmt.Errorf("Target: %w: kind %q", ErrUnsupported, k)
	}
	w.extra, err = readParsedObject(o, &w, "kind Path", "kind Path")
	if err != nil {
		return err
	}
	*t = Target(w)
	return nil
}
func (t Target) MarshalJSON() ([]byte, error) {
	if t.Kind != "local" {
		return nil, fmt.Errorf("Target: %w: kind %q", ErrUnsupported, t.Kind)
	}
	if err := validText(t.Path); err != nil {
		return nil, err
	}
	type wire Target
	return writeObject(wire(t), t.extra)
}
func (t *Trigger) UnmarshalJSON(data []byte) error {
	type wire Trigger
	var w wire
	o, k, err := parseObjectKind(data)
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
	w.extra, err = readParsedObject(o, &w, fields, required)
	if err != nil {
		return err
	}
	*t = Trigger(w)
	return nil
}
func (t Trigger) MarshalJSON() ([]byte, error) {
	switch t.Kind {
	case "manual":
	case "scheduled":
		if t.CronExpression == nil {
			return nil, fmt.Errorf("missing CronExpression")
		}
	case "watch":
		if t.SettleSeconds == nil {
			return nil, fmt.Errorf("Trigger: watch requires an explicit settle period in memory")
		}
	default:
		return nil, fmt.Errorf("Trigger: %w: kind %q", ErrUnsupported, t.Kind)
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
	o, k, err := parseObjectKind(data)
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
	w.extra, err = readParsedObject(o, &w, fields, fields)
	if err != nil {
		return err
	}
	if k == "path" {
		s := normalizeFilterPath(*w.Prefix)
		w.Prefix = &s
	}
	if k == "extension" {
		raw := *w.Extension
		s := normalizeExtension(raw)
		if normalizeExtension(s) != s {
			w.extensionWire = raw
		}
		w.Extension = &s
	}
	if k == "wildcard" {
		s := normalizeFilterPath(*w.Pattern)
		w.Pattern = &s
	}
	*r = Rule(w)
	return nil
}
func (r Rule) MarshalJSON() ([]byte, error) {
	switch r.Kind {
	case "all":
	case "path":
		if r.Prefix == nil {
			return nil, fmt.Errorf("missing Prefix")
		}
	case "extension":
		if r.Extension == nil {
			return nil, fmt.Errorf("missing Extension")
		}
	case "wildcard", "regex":
		if r.Pattern == nil {
			return nil, fmt.Errorf("missing Pattern")
		}
	case "allOf", "anyOf":
		if r.Rules == nil || *r.Rules == nil {
			return nil, fmt.Errorf("missing Rules")
		}
	case "not":
		if r.Rule == nil {
			return nil, fmt.Errorf("missing Rule")
		}
	default:
		return nil, fmt.Errorf("Filter: %w: kind %q", ErrUnsupported, r.Kind)
	}
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
	w := wire(r)
	if r.Kind == "extension" && r.Extension != nil {
		canonical := *r.Extension
		if strings.HasPrefix(canonical, ".") {
			return nil, fmt.Errorf("extension canonical value cannot start with dot: %w", ErrUnsupported)
		}
		if normalizeExtension(canonical) != canonical {
			raw := r.extensionWire
			if raw == "" || normalizeExtension(raw) != canonical {
				raw = "*." + canonical
			}
			w.Extension = &raw // Decode this wire spelling once to recover the exact literal canonical suffix.
		}
	}
	return writeObject(w, r.extra)
}
