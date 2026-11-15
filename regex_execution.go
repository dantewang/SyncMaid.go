package main

import (
	"errors"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/taskconfig"
)

type reviewBundle struct {
	reviews []filefilter.ReviewedRegex
	err     error
}

func (l *savedTaskLibrary) restoreReviews() {
	l.restored = map[taskconfig.ID]map[taskconfig.ID]reviewBundle{}
	seen := map[filefilter.RegexOrigin]bool{}
	for _, saved := range l.reviews {
		origin := saved.Origin
		t, err := l.find(origin.TaskID)
		if err != nil {
			continue
		} // Deleted tasks never grant another task permission.
		byTarget := l.restored[t.ID]
		if byTarget == nil {
			byTarget = map[taskconfig.ID]reviewBundle{}
			l.restored[t.ID] = byTarget
		}
		bundle := byTarget[origin.DestinationID]
		if seen[origin] {
			bundle.err = filefilter.ErrStaleReview
		} else {
			review, restoreErr := filefilter.RestoreConfirmation(t, saved)
			bundle.err = errors.Join(bundle.err, restoreErr)
			if restoreErr == nil {
				bundle.reviews = append(bundle.reviews, review)
			}
		}
		seen[origin] = true
		byTarget[origin.DestinationID] = bundle
	}
}
func (l *savedTaskLibrary) request(t taskconfig.Task) (manualTaskRequest, error) {
	q, err := multipleRequestFromSaved(t)
	if err != nil {
		return q, err
	}
	if bundles := l.restored[t.ID]; len(bundles) > 0 {
		q.regexTask = &t
		q.regexReviews = bundles
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
func prepareReviewedRequest(q manualTaskRequest) manualTaskRequest {
	if q.regexTask == nil {
		return q
	}
	q.Destinations = append([]manualDestination(nil), q.Destinations...)
	if manualKind(q.Kind) == taskconfig.Moving {
		reviews := map[taskconfig.ID][]filefilter.ReviewedRegex{}
		var err error
		for id, bundle := range q.regexReviews {
			reviews[id] = bundle.reviews
			err = errors.Join(err, bundle.err)
		}
		var matchers []*filefilter.Matcher
		if err == nil {
			matchers, err = filefilter.CompileReviewedMove(*q.regexTask, reviews)
		}
		for i := range q.Destinations {
			q.Destinations[i].filterError = err
			if err == nil {
				q.Destinations[i].matcher = matchers[i]
			}
		}
	} else {
		for i, d := range q.Destinations {
			bundle := q.regexReviews[d.ID]
			m, err := filefilter.CompileReviewedTarget(*q.regexTask, d.ID, bundle.reviews)
			q.Destinations[i].matcher = m
			q.Destinations[i].filterError = errors.Join(bundle.err, err)
		}
	}
	return q
}
