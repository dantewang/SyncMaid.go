package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type commonSettings struct {
	CloseToTray    bool
	StartMinimized bool
}
type commonSettingsStore struct {
	path     string
	value    commonSettings
	baseline historyVersion
	err      error
}

func loadCommonSettings(data string) *commonSettingsStore {
	s := &commonSettingsStore{path: filepath.Join(data, "settings-go.json")}
	s.baseline, s.err = readHistoryVersion(s.path)
	if s.err != nil || s.baseline.info == nil {
		return s
	}
	if len(s.baseline.data) > 4096 {
		s.err = errors.New("settings exceed 4096 bytes")
		return s
	}
	d := json.NewDecoder(bytes.NewReader(s.baseline.data))
	d.DisallowUnknownFields()
	var value *commonSettings
	s.err = d.Decode(&value)
	if s.err == nil {
		var tail any
		if d.Decode(&tail) != io.EOF {
			s.err = errors.New("extra settings data")
		}
	}
	if s.err == nil && value == nil {
		s.err = errors.New("settings must be an object")
	}
	if s.err == nil {
		s.value = *value
	}
	return s
}
func (s *commonSettingsStore) save(value commonSettings) error {
	if s.err != nil {
		return s.err
	}
	current, err := readHistoryVersion(s.path)
	if err == nil && ((current.info == nil) != (s.baseline.info == nil) || !bytes.Equal(current.data, s.baseline.data) || (current.info != nil && !os.SameFile(current.info, s.baseline.info))) {
		err = errors.New("settings changed externally; reload before saving")
	}
	if err != nil {
		s.err = err
		return err
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	info, err := writeRunHistory(s.path, body, replaceRunHistory)
	if err != nil {
		s.err = err
		return err
	}
	s.value = value
	s.baseline = historyVersion{data: body, info: info}
	return nil
}
