package adminapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"localgate/internal/core"
)

const terminalStreamHeartbeat = 15 * time.Second

func (a *API) writeFileTerminalStream(w http.ResponseWriter, r *http.Request, sessionID, workspaceID string, cursor, maxItems int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	updates, unsubscribe, err := a.App.Sessions.SubscribeForWorkspace(sessionID, workspaceID)
	if err != nil {
		writeFileTerminalError(w, err)
		return
	}
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	heartbeat := time.NewTicker(terminalStreamHeartbeat)
	defer heartbeat.Stop()
	forceEvent := true
	for {
		payload, readErr := a.readFileTerminal(sessionID, workspaceID, cursor, maxItems)
		if readErr != nil {
			_ = writeTerminalSSEError(w, readErr)
			flusher.Flush()
			return
		}
		if forceEvent || len(payload.Items) > 0 || payload.CursorExpired || terminalSessionEnded(payload.Session.Status) {
			if err := writeTerminalSSEEvent(w, "terminal", payload.NextOffset, payload); err != nil {
				return
			}
			flusher.Flush()
			forceEvent = false
		}
		cursor = payload.NextOffset
		if terminalSessionEnded(payload.Session.Status) && len(payload.Items) < maxItems {
			return
		}
		if len(payload.Items) == maxItems {
			continue
		}

		select {
		case <-r.Context().Done():
			return
		case _, open := <-updates:
			if !open {
				return
			}
			forceEvent = true
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeTerminalSSEEvent(w http.ResponseWriter, event string, id int, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", event, id, data)
	return err
}

func writeTerminalSSEError(w http.ResponseWriter, err error) error {
	payload := map[string]string{"message": err.Error()}
	return writeTerminalSSEEvent(w, "error", 0, payload)
}

func terminalSessionEnded(status core.SessionStatus) bool {
	return status == core.SessionExited || status == core.SessionFailed || status == core.SessionClosed
}
