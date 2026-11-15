package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/taskconfig"
)

const documentFormat = "SyncMaidGoTasks"

type taskDocument struct {
	Format             string
	Version            int
	Tasks              json.RawMessage
	RegexConfirmations []filefilter.RegexConfirmation
}

// NewVersioned is the Go application document. New retains the legacy array
// contract. This document is never written to the Rust task/backup paths.
func NewVersioned(path string) (*Store, error) {
	s, err := New(path)
	if err != nil {
		return nil, err
	}
	s.versioned = true
	s.io.read = func(path string) version { return readVersionFormat(path, true) }
	return s, nil
}
func decodeDocument(b []byte, versioned bool) ([]taskconfig.Task, []filefilter.RegexConfirmation, error) {
	if versioned && len(b) > 8<<20 {
		return nil, nil, errors.New("Go task document exceeds 8 MiB")
	}
	if !versioned || bytes.HasPrefix(bytes.TrimSpace(b), []byte("[")) {
		tasks, err := taskconfig.Decode(b)
		return tasks, nil, err
	}
	if len(b) > 8<<20 || !utf8.Valid(b) {
		return nil, nil, errors.New("invalid or oversized Go task document")
	}
	var d taskDocument
	fields, err := uniqueDocumentMembers(b)
	if err != nil {
		return nil, nil, err
	}
	var records []json.RawMessage
	if err = json.Unmarshal(fields["RegexConfirmations"], &records); err != nil {
		return nil, nil, err
	}
	for _, record := range records {
		members, e := uniqueDocumentMembers(record)
		if e != nil {
			return nil, nil, e
		}
		if _, e = uniqueDocumentMembers(members["Origin"]); e != nil {
			return nil, nil, e
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return nil, nil, err
	}
	var tail any
	if decoder.Decode(&tail) != io.EOF {
		return nil, nil, errors.New("extra Go task document data")
	}
	if d.Format != documentFormat || d.Version != 1 || d.RegexConfirmations == nil || len(d.RegexConfirmations) > 4096 {
		return nil, nil, errors.New("unsupported Go task document version or confirmations")
	}
	tasks, err := taskconfig.Decode(d.Tasks)
	return tasks, d.RegexConfirmations, err
}

// Authorization objects reject duplicate members without interpreting opaque
// unknown task payloads, whose preservation contract stays in taskconfig.
func uniqueDocumentMembers(b []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, errors.New("expected Go task document object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, err
		}
		key := token.(string)
		if _, ok := fields[key]; ok {
			return nil, errors.New("duplicate Go task document member: " + key)
		}
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return nil, err
		}
		fields[key] = raw
	}
	if _, err = d.Token(); err != nil {
		return nil, err
	}
	return fields, nil
}
func encodeDocument(tasks []byte, reviews []filefilter.RegexConfirmation) ([]byte, error) {
	if reviews == nil {
		reviews = []filefilter.RegexConfirmation{}
	}
	b, err := json.MarshalIndent(taskDocument{documentFormat, 1, tasks, reviews}, "", "  ")
	if err != nil {
		return nil, err
	}
	_, _, err = decodeDocument(b, true)
	return b, err
}
func (s *Snapshot) Confirmations() []filefilter.RegexConfirmation {
	if s == nil {
		return nil
	}
	return append([]filefilter.RegexConfirmation(nil), s.reviews...)
}
func (s *Snapshot) SaveConfirmations(tasks []taskconfig.Task, reviews []filefilter.RegexConfirmation) (*Snapshot, error) {
	if s == nil || s.store == nil || !s.store.versioned {
		return nil, ErrBlocked
	}
	return s.save(tasks, reviews)
}
