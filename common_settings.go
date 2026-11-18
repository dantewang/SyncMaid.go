package main

import (
	"encoding/json"
	"errors"
	"path/filepath"

	"syncmaid/internal/atomicfile"
)

type commonSettings struct {
	CloseToTray    bool
	StartMinimized bool
}
type commonSettingsStore struct {
	path     string
	value    commonSettings
	baseline atomicfile.Version
	err      error
}

func loadCommonSettings(data string) *commonSettingsStore {
	s := &commonSettingsStore{path: filepath.Join(data, "settings-go.json")}
	s.baseline, s.err = atomicfile.ReadRegular(s.path, 4096)
	if s.err != nil || s.baseline.Info == nil {
		return s
	}
	value, err := atomicfile.DecodeStrictJSONObject[commonSettings](s.baseline.Data)
	if err != nil {
		s.err = err
		return s
	}
	s.value = *value
	return s
}
func (s *commonSettingsStore) save(value commonSettings) error {
	if s.err != nil {
		return s.err
	}
	current, err := atomicfile.ReadRegular(s.path, 4096)
	if err == nil && !atomicfile.SameVersion(current, s.baseline) {
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
	info, err := atomicfile.Publish(s.path, body, ".syncmaid-settings-*.tmp", nil)
	if err != nil {
		s.err = err
		return err
	}
	s.value = value
	s.baseline = atomicfile.Version{Data: body, Info: info}
	return nil
}
