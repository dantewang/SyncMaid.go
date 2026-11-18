package syncengine

import (
	"errors"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/taskconfig"
)

type ReviewBundle struct {
	Reviews []filefilter.ReviewedRegex
	Err     error
}

func (q ManualTaskRequest) WithPreservedRegex(other ManualTaskRequest, taskID taskconfig.ID, taskName string) ManualTaskRequest {
	if other.regexTask == nil || (taskName == other.regexTask.Name && taskID == other.regexTask.ID) {
		q.regexTask = other.regexTask
		q.regexReviews = other.regexReviews
	}
	return q
}

func PrepareReviewedRequest(q ManualTaskRequest) ManualTaskRequest {
	if q.regexTask == nil {
		return q
	}
	q.Destinations = append([]ManualDestination(nil), q.Destinations...)
	if ManualKind(q.Kind) == taskconfig.Moving {
		reviews := map[taskconfig.ID][]filefilter.ReviewedRegex{}
		var err error
		for id, bundle := range q.regexReviews {
			reviews[id] = bundle.Reviews
			err = errors.Join(err, bundle.Err)
		}
		var matchers []*filefilter.Matcher
		if err == nil {
			matchers, err = filefilter.CompileReviewedMove(*q.regexTask, reviews)
		}
		for i := range q.Destinations {
			q.Destinations[i].FilterError = err
			if err == nil {
				q.Destinations[i].Matcher = matchers[i]
			}
		}
	} else {
		for i, d := range q.Destinations {
			bundle := q.regexReviews[d.ID]
			m, err := filefilter.CompileReviewedTarget(*q.regexTask, d.ID, bundle.Reviews)
			q.Destinations[i].Matcher = m
			q.Destinations[i].FilterError = errors.Join(bundle.Err, err)
		}
	}
	return q
}
