package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vector76/tgask/internal/model"
)

// Queuer is satisfied by *queue.Queue
type Queuer interface {
	Submit(prompt string, timeout time.Duration) string
	GetJob(id string) (*model.Job, bool)
}

// Notifier is satisfied by *telegram.Telegram
type Notifier interface {
	SendNotification(text string) error
}

type Config struct {
	Token   string
	Version string
}

type Server struct {
	cfg      Config
	queue    Queuer
	notifier Notifier
	router   chi.Router
}

func New(cfg Config, queue Queuer, notifier Notifier) *Server {
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

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request)    { w.WriteHeader(501) }
func (s *Server) handleResult(w http.ResponseWriter, r *http.Request) { w.WriteHeader(501) }
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request)   { w.WriteHeader(501) }
