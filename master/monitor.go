package master

import (
	"encoding/json"
	"net/http"
	"time"

	"Bard/internal/logger"
)

// Monitor is the master's HTTP monitoring server. It exposes /health and
// /status, which aggregates live state from all registered child instances.
type Monitor struct {
	dispatcher *Dispatcher
	startTime  time.Time
}

func NewMonitor(d *Dispatcher) *Monitor {
	return &Monitor{dispatcher: d, startTime: time.Now()}
}

func (m *Monitor) Listen(addr string) error {
	log := logger.With("monitor")
	mux := http.NewServeMux()
	mux.HandleFunc("/health", m.handleHealth)
	mux.HandleFunc("/status", m.handleStatus)
	log.Info().Str("address", addr).Msg("listening")
	return http.ListenAndServe(addr, mux)
}

func (m *Monitor) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (m *Monitor) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"uptime":   time.Since(m.startTime).Round(time.Second).String(),
		"children": m.dispatcher.Snapshots(),
	})
}
