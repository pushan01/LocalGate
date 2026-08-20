package core

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

type TaskFilter struct {
	SessionID     string
	Transport     string
	Tool          string
	Status        Status
	Requester     string
	ApprovalID    string
	CreatedAfter  time.Time
	CreatedBefore time.Time
	Limit         int
	Cursor        string
}

type TaskPage struct {
	Items      []Task `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type taskCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

var ErrInvalidTaskCursor = errors.New("invalid task cursor")

func (m *Manager) List(filter TaskFilter) (TaskPage, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var cursor taskCursor
	if strings.TrimSpace(filter.Cursor) != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
		if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.ID == "" || cursor.CreatedAt.IsZero() {
			return TaskPage{}, ErrInvalidTaskCursor
		}
	}

	m.mu.Lock()
	items := make([]Task, 0, len(m.tasks))
	for _, task := range m.tasks {
		snapshot := cloneTask(*task)
		if !matchesTaskFilter(snapshot, filter) {
			continue
		}
		if cursor.ID != "" && !taskAfterCursor(snapshot, cursor) {
			continue
		}
		items = append(items, snapshot)
	}
	m.mu.Unlock()

	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	page := TaskPage{}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[len(page.Items)-1]
		payload, _ := json.Marshal(taskCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(payload)
	} else {
		page.Items = items
	}
	return page, nil
}

// Count returns the number of tasks matching the filter. Pagination fields are
// ignored so callers can compute dashboard totals without loading task bodies.
func (m *Manager) Count(filter TaskFilter) int {
	filter.Limit = 0
	filter.Cursor = ""
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, task := range m.tasks {
		if matchesTaskFilter(*task, filter) {
			count++
		}
	}
	return count
}

func matchesTaskFilter(task Task, filter TaskFilter) bool {
	if value := strings.TrimSpace(filter.SessionID); value != "" && task.MCPSessionID != value {
		return false
	}
	if value := strings.TrimSpace(filter.Transport); value != "" && task.Transport != value {
		return false
	}
	if value := strings.TrimSpace(filter.Tool); value != "" && task.Tool != value {
		return false
	}
	if filter.Status != "" && task.Status != filter.Status {
		return false
	}
	if value := strings.TrimSpace(filter.Requester); value != "" && task.Requester != value {
		return false
	}
	if value := strings.TrimSpace(filter.ApprovalID); value != "" && task.ApprovalID != value {
		return false
	}
	if !filter.CreatedAfter.IsZero() && task.CreatedAt.Before(filter.CreatedAfter) {
		return false
	}
	if !filter.CreatedBefore.IsZero() && task.CreatedAt.After(filter.CreatedBefore) {
		return false
	}
	return true
}

func taskAfterCursor(task Task, cursor taskCursor) bool {
	if task.CreatedAt.Before(cursor.CreatedAt) {
		return true
	}
	return task.CreatedAt.Equal(cursor.CreatedAt) && task.ID < cursor.ID
}
