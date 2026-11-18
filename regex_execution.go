package main

import (
	"errors"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
)

func (l *savedTaskLibrary) restoreReviews() {
	l.restored = map[taskconfig.ID]map[taskconfig.ID]syncengine.ReviewBundle{}
	seen := map[filefilter.RegexOrigin]bool{}
	for _, saved := range l.reviews {
		origin := saved.Origin
		t, err := l.find(origin.TaskID)
		if err != nil {
			continue
		} // Deleted tasks never grant another task permission.
		byTarget := l.restored[t.ID]
		if byTarget == nil {
			byTarget = map[taskconfig.ID]syncengine.ReviewBundle{}
			l.restored[t.ID] = byTarget
		}
		bundle := byTarget[origin.DestinationID]
		if seen[origin] {
			bundle.Err = filefilter.ErrStaleReview
		} else {
			review, restoreErr := filefilter.RestoreConfirmation(t, saved)
			bundle.Err = errors.Join(bundle.Err, restoreErr)
			if restoreErr == nil {
				bundle.Reviews = append(bundle.Reviews, review)
			}
		}
		seen[origin] = true
		byTarget[origin.DestinationID] = bundle
	}
}
func (l *savedTaskLibrary) request(t taskconfig.Task) (syncengine.ManualTaskRequest, error) {
	q, err := multipleRequestFromSaved(t)
	if err != nil {
		return q, err
	}
	if bundles := l.restored[t.ID]; len(bundles) > 0 {
		q = q.WithReviewedRegex(&t, bundles)
	}
	return q, nil
}
func (l *savedTaskLibrary) saveRegexConfirmation(review filefilter.ReviewedRegex) (*savedTaskLibrary, error) {
	saved, err := review.Confirmation()
	if err != nil {
		return nil, err
	}
	t, err := l.find(saved.Origin.TaskID)
	if err != nil {
		return nil, err
	}
	if _, err = filefilter.RestoreConfirmation(t, saved); err != nil {
		return nil, err
	}
	next := []filefilter.RegexConfirmation{}
	for _, old := range l.reviews {
		if old.Origin.TaskID == saved.Origin.TaskID {
			if old.Origin.DocumentSHA256 != saved.Origin.DocumentSHA256 {
				continue
			}
			if old.Origin.DestinationID == saved.Origin.DestinationID && old.Origin.Field == saved.Origin.Field {
				continue
			}
		}
		next = append(next, old)
	}
	next = append(next, saved)
	snapshot, err := l.snapshot.SaveConfirmations(l.tasks, next)
	if err != nil {
		return nil, err
	}
	return savedLibrary(snapshot)
}
