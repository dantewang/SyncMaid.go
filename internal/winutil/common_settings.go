package winutil

import (
	"encoding/json"
	"errors"
	"path/filepath"

	"syncmaid/internal/atomicfile"
)

type CommonSettings struct {
	CloseToTray    bool
	StartMinimized bool
}
type CommonSettingsStore struct {
	path     string
	Value    CommonSettings
	baseline atomicfile.Version
	Err      error
}

func LoadCommonSettings(data string) *CommonSettingsStore {
	s := &CommonSettingsStore{path: filepath.Join(data, "settings-go.json")}
	s.baseline, s.Err = atomicfile.ReadRegular(s.path, 4096)
	if s.Err != nil || s.baseline.Info == nil {
		return s
	}
	value, err := atomicfile.DecodeStrictJSONObject[CommonSettings](s.baseline.Data)
	if err != nil {
		s.Err = err
		return s
	}
	s.Value = *value
	return s
}
func (s *CommonSettingsStore) Save(value CommonSettings) error {
	if s.Err != nil {
		return s.Err
	}
	current, err := atomicfile.ReadRegular(s.path, 4096)
	if err == nil && !atomicfile.SameVersion(current, s.baseline) {
		err = errors.New("settings changed externally; reload before saving")
	}
	if err != nil {
		s.Err = err
		return err
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	info, err := atomicfile.Publish(s.path, body, ".syncmaid-settings-*.tmp", nil)
	if err != nil {
		s.Err = err
		return err
	}
	s.Value = value
	s.baseline = atomicfile.Version{Data: body, Info: info}
	return nil
}
