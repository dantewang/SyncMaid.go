package appcore

import (
	"errors"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
)

func (l *SavedTaskLibrary) RestoreReviews() {
	l.Restored = map[taskconfig.ID]map[taskconfig.ID]syncengine.ReviewBundle{}
	seen := map[filefilter.RegexOrigin]bool{}
	for _, saved := range l.Reviews {
		origin := saved.Origin
		t, err := l.Find(origin.TaskID)
		if err != nil {
			continue
		} // Deleted tasks never grant another task permission.
		byTarget := l.Restored[t.ID]
		if byTarget == nil {
			byTarget = map[taskconfig.ID]syncengine.ReviewBundle{}
			l.Restored[t.ID] = byTarget
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
func (l *SavedTaskLibrary) Request(t taskconfig.Task) (syncengine.ManualTaskRequest, error) {
	q, err := MultipleRequestFromSaved(t)
	if err != nil {
		return q, err
	}
	if bundles := l.Restored[t.ID]; len(bundles) > 0 {
		q = q.WithReviewedRegex(&t, bundles)
	}
	return q, nil
}
func (l *SavedTaskLibrary) SaveRegexConfirmation(review filefilter.ReviewedRegex) (*SavedTaskLibrary, error) {
	saved, err := review.Confirmation()
	if err != nil {
		return nil, err
	}
	t, err := l.Find(saved.Origin.TaskID)
	if err != nil {
		return nil, err
	}
	if _, err = filefilter.RestoreConfirmation(t, saved); err != nil {
		return nil, err
	}
	next := []filefilter.RegexConfirmation{}
	for _, old := range l.Reviews {
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
	snapshot, err := l.Snapshot.SaveConfirmations(l.Tasks, next)
	if err != nil {
		return nil, err
	}
	return SavedLibrary(snapshot)
}
