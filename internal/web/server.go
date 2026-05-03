package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/eike-hass/infra-mngmt/internal/compose"
	"github.com/eike-hass/infra-mngmt/internal/docker"
	"github.com/eike-hass/infra-mngmt/internal/source"
)

type Server struct {
	sources     []source.Source
	compose     []*compose.Client
	booters     map[string]*compose.Bootstrapper // keyed by client name
	docker      *docker.Client                   // nil if Docker unavailable
	mux         *chi.Mux
	token       string   // required bearer token; empty = auth disabled
	sessions    sync.Map // session ID (string) → struct{}
	entityCache entityCacheEntry
}

func New(sources []source.Source, composeCfg []ComposeEntry, token string, dc *docker.Client) *Server {
	s := &Server{
		sources: sources,
		booters: map[string]*compose.Bootstrapper{},
		token:   token,
		docker:  dc,
	}
	for _, e := range composeCfg {
		s.compose = append(s.compose, compose.New(e.Name, e.Endpoint, e.Token))
		if e.Binary != "" && e.ComposeFile != "" {
			s.booters[e.Name] = &compose.Bootstrapper{
				Name:        e.Name,
				Binary:      e.Binary,
				ComposeFile: e.ComposeFile,
				Endpoint:    e.Endpoint,
			}
		}
	}

	s.mux = chi.NewRouter()
	s.mux.Use(middleware.Logger)
	s.mux.Use(middleware.Recoverer)

	// Public routes — no auth required.
	s.mux.Get("/login", s.handleLoginGet)
	s.mux.Post("/login", s.handleLoginPost)
	s.mux.Post("/logout", s.handleLogout)

	// All other routes require authentication (when a token is configured).
	s.mux.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)

		// entity routes
		r.Get("/", s.handleIndex)
		r.Get("/api/sources", s.handleSources)
		r.Get("/api/entities", s.handleEntities)
		r.Get("/api/entity/content", s.handleEntityContent)
		r.Post("/api/entity", s.handleEntityWrite)
		r.Get("/partials/entity", s.handleEntityPreview)

		// services routes
		r.Get("/partials/services", s.handleServicesPartial)
		r.Post("/process/start", s.handleProcessStart)   // ?instance=&process=
		r.Post("/process/stop", s.handleProcessStop)     // ?instance=&process=
		r.Post("/process/restart", s.handleProcessRestart)
		r.Post("/compose/start", s.handleComposeStart)   // ?instance=  — bootstrap
		r.Get("/partials/logs", s.handleProcessLogs)     // ?instance=&process=
		r.Post("/api/refresh", s.handleRefresh)
		r.Get("/api/containers", s.handleContainers)
		r.Post("/api/container/start", s.handleContainerStart)
		r.Post("/api/container/stop", s.handleContainerStop)
		r.Get("/api/container/logs-stream", s.handleContainerLogsStream)
	})

	return s
}

// ComposeEntry is a flat struct passed from main to avoid importing config.
type ComposeEntry struct {
	Name, Endpoint, Token, Binary, ComposeFile string
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ── auth middleware ───────────────────────────────────────────────────────────

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie("im_session"); err == nil {
			if _, ok := s.sessions.Load(c.Value); ok {
				next.ServeHTTP(w, r)
				return
			}
		}
		target := "/login?next=" + url.QueryEscape(r.URL.RequestURI())
		http.Redirect(w, r, target, http.StatusSeeOther)
	})
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	// Already logged in — skip straight to the app.
	if s.token == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if c, err := r.Cookie("im_session"); err == nil {
		if _, ok := s.sessions.Load(c.Value); ok {
			next := r.URL.Query().Get("next")
			if next == "" || !strings.HasPrefix(next, "/") {
				next = "/"
			}
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
	}
	tmpl := template.Must(template.New("login").Parse(loginHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, map[string]string{"Next": r.URL.Query().Get("next"), "Error": ""})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	submitted := r.FormValue("token")
	if subtle.ConstantTimeCompare([]byte(submitted), []byte(s.token)) != 1 {
		tmpl := template.Must(template.New("login").Parse(loginHTML))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		tmpl.Execute(w, map[string]string{"Next": r.FormValue("next"), "Error": "invalid token"})
		return
	}
	sid := make([]byte, 16)
	rand.Read(sid)
	sessionID := hex.EncodeToString(sid)
	s.sessions.Store(sessionID, struct{}{})
	http.SetCookie(w, &http.Cookie{
		Name:     "im_session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	next := r.FormValue("next")
	if next == "" || !strings.HasPrefix(next, "/") {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("im_session"); err == nil {
		s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "im_session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
