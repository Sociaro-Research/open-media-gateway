package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/limits"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type limitedKeys struct {
	apikey.Repository
	policies limits.Reader
	limiter  limits.Limiter
}

// WithRequestLimits binds shared admission accounting to the authenticated key
// store. The production server requires both policy storage and Redis.
func WithRequestLimits(keys apikey.Repository, policies limits.Reader, limiter limits.Limiter) apikey.Repository {
	if keys == nil || policies == nil || limiter == nil {
		panic("request limits dependencies are required")
	}
	return &limitedKeys{keys, policies, limiter}
}
func (k *limitedKeys) CheckLimits(ctx context.Context, org, operation string) error {
	p, err := k.policies.RequestLimits(ctx, org)
	if err != nil {
		return err
	}
	key := ""
	if identity.ActorID(ctx) == identity.SystemOwnerID {
		key = "bootstrap"
	}
	if id := identity.APIKeyID(ctx); id != nil {
		key = *id
	}
	return k.limiter.Take(ctx, limits.Buckets(p.Effective, org, identity.ActorID(ctx), key, operation))
}
func limitError(w http.ResponseWriter, err error) {
	var quota *limits.Exceeded
	if errors.As(err, &quota) {
		w.Header().Set("Retry-After", strconv.FormatInt(max(1, int64((quota.RetryAfter+time.Second-1)/time.Second)), 10))
		writeError(w, 429, "rate_limit_exceeded", quota.Error())
		return
	}
	if errors.Is(err, limits.ErrActiveKeys) {
		writeError(w, 409, "active_key_limit", err.Error())
		return
	}
	writeError(w, 503, "limits_unavailable", "Request limits could not be checked. Please retry.")
}
func (s *Server) checkLimits(w http.ResponseWriter, r *http.Request, org, operation string) bool {
	if checker, ok := s.keys.(interface {
		CheckLimits(context.Context, string, string) error
	}); ok {
		if err := checker.CheckLimits(r.Context(), org, operation); err != nil {
			limitError(w, err)
			return false
		}
	}
	return true
}
func requestOperation(r *http.Request) string {
	if r.Method == "POST" && isGenerationPath(r.URL.Path) && !strings.HasSuffix(r.URL.Path, "/estimate") {
		return "generation"
	}
	if r.Method == "GET" || r.Method == "HEAD" {
		// Route patterns exclude content/downloads and prevent arbitrary URL suffixes
		// from bypassing the shared status/list quota.
		switch r.Pattern {
		case "GET /v1/generations", "GET /v1/generations/{id}", "GET /v1/videos/{id}":
			return "read"
		}
	}
	return ""
}
func (s *Server) requestLimits(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if op := requestOperation(r); op != "" && !s.checkLimits(w, r, credentialOrganization(r.Context()), op) {
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) limitSettings(w http.ResponseWriter, r *http.Request) {
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	if !admin {
		writeError(w, 403, "forbidden", "administrator access is required")
		return
	}
	repo, ok := s.system.(limits.Repository)
	if !ok {
		writeError(w, 503, "unavailable", "request limit settings are unavailable")
		return
	}
	org := r.PathValue("org")
	if org != "" {
		if _, err := uuid.Parse(org); err != nil {
			writeError(w, 400, "invalid_request", "invalid organization ID")
			return
		}
	}
	if bound := credentialOrganization(r.Context()); bound != "" && org != "" && bound != org {
		writeError(w, 403, "forbidden", "API key belongs to another organization")
		return
	}
	var p limits.Policy
	if r.Method == "GET" {
		p, err = repo.LimitSettings(r.Context(), identity.ActorID(r.Context()), org)
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		defer r.Body.Close()
		var input limits.Config
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if e := d.Decode(&input); e != nil || d.Decode(&struct{}{}) != io.EOF {
			writeError(w, 400, "invalid_request", "send one quota configuration")
			return
		}
		if e := input.Validate(org == ""); e != nil {
			writeError(w, 400, "invalid_request", e.Error())
			return
		}
		p, err = repo.SaveLimits(r.Context(), identity.ActorID(r.Context()), org, input)
	}
	if err != nil {
		systemError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, p)
}
