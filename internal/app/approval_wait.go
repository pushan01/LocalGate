package app

import (
	"context"
	"errors"
	"time"

	"localgate/internal/core"
)

// WaitForApprovalDecision briefly keeps a synchronous tool invocation open
// while its gated task is awaiting approval. A false resolved result means the
// original approval and task remain pending and may be handled later.
func (a *App) WaitForApprovalDecision(ctx context.Context, submission Submission) (*core.Task, bool, error) {
	if submission.Task == nil {
		return nil, false, errors.New("tool task was not created")
	}
	if submission.Approval == nil {
		task, err := a.Tasks.Get(submission.Task.ID)
		return task, true, err
	}
	wait := time.Duration(a.Approval.PendingWaitSeconds) * time.Second
	if wait < 0 {
		wait = 0
	}
	latest, err := a.Tasks.Get(submission.Task.ID)
	if err != nil {
		return nil, false, err
	}
	if latest.Status != core.StatusAwaitingApproval {
		return latest, true, nil
	}
	if wait == 0 {
		return latest, false, nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return latest, false, ctx.Err()
		case <-timer.C:
			// Check once more after the timer fires so an approval racing with the
			// deadline is not incorrectly reported as still pending.
			current, getErr := a.Tasks.Get(submission.Task.ID)
			if getErr != nil {
				return nil, false, getErr
			}
			return current, current.Status != core.StatusAwaitingApproval, nil
		case <-ticker.C:
			current, getErr := a.Tasks.Get(submission.Task.ID)
			if getErr != nil {
				return nil, false, getErr
			}
			latest = current
			if current.Status != core.StatusAwaitingApproval {
				return current, true, nil
			}
		}
	}
}
