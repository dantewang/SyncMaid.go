package main

import "syncmaid/internal/taskconfig"

type batchPhase uint8

const (
	batchIdle batchPhase = iota
	batchServing
)

// UI-owned order fixed at the user's click; no parallel executor or retries.
type manualBatch struct {
	phase   batchPhase
	pending []taskconfig.ID
}

func (b *manualBatch) clear() { b.phase = batchIdle; b.pending = nil }
func (b *manualBatch) begin(ids []taskconfig.ID) {
	b.phase = batchServing
	b.pending = append([]taskconfig.ID(nil), ids...)
}
func (b *manualBatch) next() (taskconfig.ID, bool) {
	if b.phase != batchServing || len(b.pending) == 0 {
		return "", false
	}
	id := b.pending[0]
	b.pending = b.pending[1:]
	return id, true
}

func (a *application) startBatch() {
	if !a.cardActionAllowed() || a.hasModalOpen() || a.manual.quitPending {
		return
	}
	a.batch.begin(taskIDs(a.tasks.tasks))
	a.advanceBatch()
}
func (a *application) advanceBatch() { a.advanceBatchUsing(a.startTaskRun) }
func (a *application) advanceBatchUsing(start func(taskconfig.ID, string, manualTaskRequest, bool)) {
	if a.batch.phase != batchServing || a.manual.busy() || a.hasModalOpen() {
		return
	}
	if a.manual.quitPending || a.manual.dirty || a.taskError != nil || a.tasks == nil {
		a.batch.clear()
		return
	}
	for {
		id, ok := a.batch.next()
		if !ok {
			a.batch.clear()
			a.drainWatches()
			return
		}
		t, err := a.tasks.find(id)
		if err == nil {
			q, e := a.tasks.request(t)
			err = e
			if err == nil {
				a.selectManual(id)
				if len(q.Destinations) == 0 {
					a.startTaskRun(id, t.Name, q, false)
					continue
				}
				start(id, t.Name, q, false)
				return
			}
		}
		if a.results == nil {
			a.results = map[taskconfig.ID]savedTaskResult{}
		}
		goals := multipleResult{}
		for _, d := range t.Destinations {
			goals[d.ID] = destinationResult{ID: d.ID, Name: d.Name, State: destinationFailed, Error: err.Error()}
		}
		a.results[id] = savedTaskResult{outcome: "failed", message: messagef("准备失败，任务未执行: %s", err.Error()), destinations: goals}
	}
}
