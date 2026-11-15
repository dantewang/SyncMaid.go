package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/taskconfig"
)

const importBytes = 4 << 20
const importTasks = 256

// The preview owns the decoded snapshot. Confirmation never reopens the source.
type legacyImport struct {
	path   string
	tasks  []taskconfig.Task
	issues []error
}

func readLegacyImport(path string, library *savedTaskLibrary) (*legacyImport, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(io.LimitReader(f, importBytes+1))
	err = errors.Join(readErr, f.Close())
	if err != nil {
		return nil, err
	}
	if len(b) > importBytes {
		return nil, errors.New("import exceeds 4 MiB")
	}
	tasks, err := taskconfig.Decode(b)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 || len(tasks) > importTasks {
		return nil, errors.New("import requires 1–256 tasks")
	}
	for i := range tasks {
		t := &tasks[i]
		if len(t.Destinations) > 256 {
			return nil, fmt.Errorf("tasks[%d]: exceeds 256 destinations", i)
		}
		if t.Trigger.Kind != "manual" {
			if t.Trigger.HasUnknownFields() {
				return nil, fmt.Errorf("tasks[%d].Trigger: unknown members prevent safe Manual conversion", i)
			}
			t.Trigger = taskconfig.Trigger{Kind: "manual"}
		}
	}
	p := &legacyImport{path: path, tasks: tasks}
	if err := library.checkImport(p); err != nil {
		return nil, err
	}
	for _, t := range tasks {
		p.issues = append(p.issues, importedTaskIssue(t))
	}
	return p, nil
}

func importContent(t taskconfig.Task) ([]byte, error) {
	// Missing legacy IDs are generated on decode. Compare canonical content too
	// so selecting such a file twice cannot silently append duplicate tasks.
	const zero taskconfig.ID = "00000000-0000-0000-0000-000000000000"
	t.ID = zero
	t.Destinations = append([]taskconfig.Destination(nil), t.Destinations...)
	for i := range t.Destinations {
		t.Destinations[i].ID = zero
	}
	return taskconfig.Encode([]taskconfig.Task{t})
}
func (l *savedTaskLibrary) checkImport(p *legacyImport) error {
	if l == nil || l.snapshot == nil || p == nil {
		return errors.New("task library unavailable")
	}
	if len(l.tasks)+len(p.tasks) > importTasks {
		return errors.New("combined task count exceeds 256")
	}
	ids := map[taskconfig.ID]bool{}
	goals := map[taskconfig.ID]bool{}
	contents := [][]byte{}
	for _, t := range l.tasks {
		ids[t.ID] = true
		for _, d := range t.Destinations {
			goals[d.ID] = true
		}
		b, err := importContent(t)
		if err != nil {
			return err
		}
		contents = append(contents, b)
	}
	for i, t := range p.tasks {
		if ids[t.ID] {
			return fmt.Errorf("tasks[%d].Id: duplicate task ID %s", i, t.ID)
		}
		ids[t.ID] = true
		for j, d := range t.Destinations {
			if goals[d.ID] {
				return fmt.Errorf("tasks[%d].Destinations[%d].Id: duplicate destination ID %s", i, j, d.ID)
			}
			goals[d.ID] = true
		}
		b, err := importContent(t)
		if err != nil {
			return err
		}
		for _, old := range contents {
			if bytes.Equal(old, b) {
				return fmt.Errorf("tasks[%d]: duplicate task content; already imported or repeated in this file", i)
			}
		}
		contents = append(contents, b)
	}
	return nil
}
func (l *savedTaskLibrary) appendImport(p *legacyImport) (*savedTaskLibrary, error) {
	if err := l.checkImport(p); err != nil {
		return nil, err
	}
	tasks, err := l.snapshot.Tasks()
	if err != nil {
		return nil, err
	}
	tasks = append(tasks, p.tasks...)
	next, err := l.snapshot.Save(tasks)
	if err != nil {
		return nil, err
	}
	return savedLibrary(next)
}
func importedTaskIssue(t taskconfig.Task) error {
	if _, err := taskconfig.Validate([]taskconfig.Task{t}); err != nil {
		return err
	}
	if _, err := multipleRequestFromSaved(t); err != nil {
		return err
	}
	for i, d := range t.Destinations {
		if _, err := filefilter.Compile(d.Filters); err != nil {
			return fmt.Errorf("Destinations[%d] %s: %w", i, d.Name, err)
		}
	}
	return nil
}
