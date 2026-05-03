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
				TokenFile:   e.TokenFile,
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
	s.mux.Get("/favicon.svg", handleFavicon)

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
		r.Get("/partials/entity-list", s.handleEntityListPartial)

		// services routes
		r.Get("/partials/services", s.handleServicesPartial)
		r.Post("/process/start", s.handleProcessStart) // ?instance=&process=
		r.Post("/process/stop", s.handleProcessStop)   // ?instance=&process=
		r.Post("/process/restart", s.handleProcessRestart)
		r.Post("/compose/start", s.handleComposeStart) // ?instance=  — bootstrap
		r.Get("/partials/logs", s.handleProcessLogs)   // ?instance=&process=
		r.Post("/api/refresh", s.handleRefresh)
		r.Get("/api/containers", s.handleContainers)
		r.Post("/api/container/start", s.handleContainerStart)
		r.Post("/api/container/stop", s.handleContainerStop)
		r.Get("/api/container/events-stream", s.handleContainerEventsStream)
	})

	return s
}

// ComposeEntry is a flat struct passed from main to avoid importing config.
type ComposeEntry struct {
	Name, Endpoint, Token, Binary, ComposeFile, TokenFile string
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
	_ = tmpl.Execute(w, map[string]string{"Next": r.URL.Query().Get("next"), "Error": ""})
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
		_ = tmpl.Execute(w, map[string]string{"Next": r.FormValue("next"), "Error": "invalid token"})
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

// faviconSVG is served at /favicon.svg — dual interlocking hexagons (host + container).
const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
  <rect width="32" height="32" rx="6" fill="#0c0c0c"/>
  <polygon points="9,3 18,3 22.5,10.5 18,18 9,18 4.5,10.5" fill="oklch(68% 0.18 200)" opacity="0.95"/>
  <polygon points="14,14 23,14 27.5,21.5 23,29 14,29 9.5,21.5" fill="none" stroke="oklch(68% 0.18 200)" stroke-width="1.5" opacity="0.55"/>
</svg>`

func handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(faviconSVG))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("im_session"); err == nil {
		s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "im_session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
