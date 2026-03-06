package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vector76/tgask/internal/model"
)

// Queuer is satisfied by *queue.Queue
type Queuer interface {
	Submit(prompt string, timeout time.Duration, plainText bool) string
	GetJob(id string) (*model.Job, bool)
}

// Notifier is satisfied by *telegram.Telegram
type Notifier interface {
	SendNotification(text string) error
}

type Config struct {
	Token             string
	Version           string
	DefaultJobTimeout time.Duration
}

type Server struct {
	cfg      Config
	queue    Queuer
	notifier Notifier
	router   chi.Router
}

func New(cfg Config, queue Queuer, notifier Notifier) *Server {
	if cfg.DefaultJobTimeout <= 0 {
		cfg.DefaultJobTimeout = 3600 * time.Second
	}
	s := &Server{cfg: cfg, queue: queue, notifier: notifier}
	s.router = chi.NewRouter()
	s.setupRoutes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) setupRoutes() {
	s.router.Get("/health", s.handleHealth)
	s.router.Get("/version", s.handleVersion)

	s.router.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Post("/api/v1/ask", s.handleAsk)
		r.Get("/api/v1/result/{id}", s.handleResult)
		r.Post("/api/v1/send", s.handleSend)
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok || token != s.cfg.Token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.cfg.Version})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt    string `json:"prompt"`
		Timeout   int    `json:"timeout"`
		PlainText bool   `json:"plain_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prompt required"})
		return
	}
	jobTimeout := s.cfg.DefaultJobTimeout
	if req.Timeout > 0 {
		jobTimeout = min(time.Duration(req.Timeout)*time.Second, s.cfg.DefaultJobTimeout)
	}
	id := s.queue.Submit(req.Prompt, jobTimeout, req.PlainText)
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) handleResult(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	job, ok := s.queue.GetJob(id)
	if !ok {
		writeJSON(w, http.StatusGone, map[string]string{"status": string(model.StatusExpired)})
		return
	}

	// Fast path: job already in terminal state
	if job.Status == model.StatusDone {
		writeJSON(w, http.StatusOK, map[string]string{"status": string(model.StatusDone), "reply": job.Reply})
		return
	}
	if job.Status == model.StatusExpired {
		writeJSON(w, http.StatusGone, map[string]string{"status": string(model.StatusExpired)})
		return
	}
	if job.Status == model.StatusFailed {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"status": string(model.StatusFailed), "error": job.Error})
		return
	}

	// Long-poll: wait for DoneCh or timeout
	waitSecs := 30
	if wStr := r.URL.Query().Get("wait"); wStr != "" {
		if n, err := strconv.Atoi(wStr); err == nil && n > 0 {
			waitSecs = n
		}
	}
	if waitSecs > 60 {
		waitSecs = 60
	}

	select {
	case <-job.DoneCh:
		switch job.Status {
		case model.StatusDone:
			writeJSON(w, http.StatusOK, map[string]string{"status": string(model.StatusDone), "reply": job.Reply})
		case model.StatusFailed:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"status": string(model.StatusFailed), "error": job.Error})
		default:
			writeJSON(w, http.StatusGone, map[string]string{"status": string(model.StatusExpired)})
		}
	case <-time.After(time.Duration(waitSecs) * time.Second):
		writeJSON(w, http.StatusAccepted, map[string]string{"status": string(job.Status)})
	case <-r.Context().Done():
		// client disconnected; nothing to write
	}
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.Message == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message required"})
		return
	}
	if err := s.notifier.SendNotification(req.Message); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
