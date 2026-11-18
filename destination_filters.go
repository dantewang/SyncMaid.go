package main

import (
	"errors"
	"fmt"

	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
)

func taskConfigurationDeletionIssue(t taskconfig.Task) error {
	if _, err := multipleRequestFromSaved(t); err != nil {
		return err
	}
	for _, d := range t.Destinations {
		if syncengine.RulesHaveUnknownFields(d.Filters) {
			return fmt.Errorf("%s 含未支持字段，不能删除其配置", d.Name)
		}
	}
	return nil
}

type filterMode uint8

const (
	filterAll filterMode = iota
	filterNone
	filterReadOnly
	filterGrouped
)

type filterEditor struct {
	Groups                 []filterDraftGroup
	MatchAll               bool
	Mode                   filterMode
	Error, Reason, RunError string
}

func (e filterEditor) rules() ([]taskconfig.Rule, error) {
	switch e.Mode {
	case filterGrouped:
		return e.groupRules()
	case filterAll:
		return syncengine.AllFileRules(), nil
	case filterNone:
		return []taskconfig.Rule{}, nil
	case filterReadOnly:
		return nil, errors.New("只读过滤规则不能改写")
	default:
		return nil, errors.New("未知过滤模式")
	}
}
