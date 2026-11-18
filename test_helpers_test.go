package main

import (
	"context"
	"errors"
	"fmt"

	"syncmaid/internal/taskconfig"
)

func (l *savedTaskLibrary) saveMultiple(id taskconfig.ID, name string, q manualTaskRequest) (*savedTaskLibrary, taskconfig.ID, error) {
	tasks, id, err := l.prepareMultiple(id, name, q)
	if err != nil {
		return nil, id, err
	}
	next, err := l.snapshot.Save(tasks)
	if err != nil {
		return nil, id, fmt.Errorf("保存任务: %w", err)
	}
	library, err := savedLibrary(next)
	return library, id, err
}

func (l *savedTaskLibrary) save(id taskconfig.ID, name string, q manualRequest) (*savedTaskLibrary, taskconfig.ID, error) {
	d := manualDestination{ID: taskconfig.NewID(), Name: "目标", Path: q.Target, Filters: allFileRules()}
	if id != "" {
		t, err := l.find(id)
		if err != nil {
			return nil, id, err
		}
		old, err := multipleRequestFromSaved(t)
		if err != nil {
			return nil, id, err
		}
		if len(old.Destinations) != 1 {
			return nil, id, errors.New("单目标保存不能缩减多目标任务")
		}
		d.ID = old.Destinations[0].ID
		d.Name = old.Destinations[0].Name
		if destinationStrategy(old.Destinations[0]) != taskconfig.AddOnly || !isAllFileRules(old.Destinations[0].Filters) {
			return nil, id, errors.New("无过滤的单目标保存接口不能覆盖过滤规则")
		}
		d.Filters = old.Destinations[0].Filters
	}
	return l.saveMultiple(id, name, manualTaskRequest{Source: q.Source, Destinations: []manualDestination{d}})
}

func requestFromSaved(t taskconfig.Task) (manualRequest, error) {
	q, err := multipleRequestFromSaved(t)
	if err != nil {
		return manualRequest{}, err
	}
	if len(q.Destinations) != 1 {
		return manualRequest{}, errors.New("需要单目标任务")
	}
	if destinationStrategy(q.Destinations[0]) != taskconfig.AddOnly || !isAllFileRules(q.Destinations[0].Filters) {
		return manualRequest{}, errors.New("无过滤的单目标接口不能忽略现有过滤规则")
	}
	return manualRequest{Source: q.Source, Target: q.Destinations[0].Path}, nil
}

func runMultiple(ctx context.Context, q manualTaskRequest, notify func(destinationResult)) (multipleResult, error) {
	return runMultipleConfirmed(ctx, q, notify, nil)
}

func runManual(ctx context.Context, q manualRequest, notify func(transferProgress)) (transferProgress, error) {
	return runManualWith(ctx, q, notify, transferIO{copy: copyChecked, publish: publishTransfer})
}

func runManualWith(ctx context.Context, q manualRequest, notify func(transferProgress), ops transferIO) (transferProgress, error) {
	return runManualWithFilters(ctx, q, notify, ops, []taskconfig.Rule{{Kind: "all"}})
}

func validateManual(ctx context.Context, q manualRequest) (result manualRequest, err error) {
	r, err := openTransferRoots(ctx, q)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, r.close(ctx)) }()
	return manualRequest{Source: r.source.path, Target: r.target.path}, nil
}
