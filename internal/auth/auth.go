package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"polar/internal/config"
)

const (
	ScopeReadTelemetry = "read:telemetry"
	ScopeReadForecast  = "read:forecast"
	ScopeReadAudit     = "read:audit"
	ScopeAdminConfig   = "admin:config"
	WildcardScope      = "*"
)

var allScopes = []string{
	ScopeReadTelemetry,
	ScopeReadForecast,
	ScopeReadAudit,
	ScopeAdminConfig,
}

type Principal struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`

	scopeSet map[string]struct{}
}

type AccessError struct {
	StatusCode int
	Message    string
}

func (e *AccessError) Error() string {
	return e.Message
}

type Auth struct {
	principals  map[string]Principal
	failureHook func(statusCode int)
}

type principalContextKey struct{}

func New(token string) *Auth {
	return &Auth{
		principals: map[string]Principal{
			token: newPrincipal("legacy-service-token", []string{WildcardScope}),
		},
	}
}

func NewFromConfig(cfg config.AuthConfig) *Auth {
	if len(cfg.Tokens) == 0 {
		return New(cfg.ServiceToken)
	}

	principals := make(map[string]Principal, len(cfg.Tokens)+1)
	if cfg.ServiceToken != "" {
		principals[cfg.ServiceToken] = newPrincipal("legacy-service-token", []string{WildcardScope})
	}
	for _, token := range cfg.Tokens {
		principals[token.Value] = newPrincipal(token.Name, token.Scopes)
	}
	return &Auth{principals: principals}
}

func (a *Auth) SetFailureHook(fn func(statusCode int)) {
	a.failureHook = fn
}

func (a *Auth) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := a.AuthenticateRequest(r)
		if err != nil {
			a.writeAccessDenied(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *Auth) Require(next http.Handler, scopes ...string) http.Handler {
	return a.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := a.AuthorizeRequest(r, scopes...); err != nil {
			a.writeAccessDenied(w, err)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (a *Auth) AuthenticateRequest(r *http.Request) (Principal, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return Principal{}, &AccessError{StatusCode: http.StatusUnauthorized, Message: "missing bearer token"}
	}

	token := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	if token == "" {
		return Principal{}, &AccessError{StatusCode: http.StatusUnauthorized, Message: "missing bearer token"}
	}

	principal, ok := a.principals[token]
	if !ok {
		return Principal{}, &AccessError{StatusCode: http.StatusUnauthorized, Message: "unauthorized"}
	}
	return principal, nil
}

func (a *Auth) AuthorizeRequest(r *http.Request, scopes ...string) error {
	if len(scopes) == 0 {
		return nil
	}

	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		var err error
		principal, err = a.AuthenticateRequest(r)
		if err != nil {
			return err
		}
	}

	if principal.HasScopes(scopes...) {
		return nil
	}
	return &AccessError{StatusCode: http.StatusForbidden, Message: "forbidden"}
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

func (p Principal) HasScopes(scopes ...string) bool {
	if len(scopes) == 0 {
		return true
	}
	if _, ok := p.scopeSet[WildcardScope]; ok {
		return true
	}
	for _, scope := range scopes {
		if _, ok := p.scopeSet[scope]; !ok {
			return false
		}
	}
	return true
}

func StatusCode(err error) int {
	var accessErr *AccessError
	if errors.As(err, &accessErr) {
		return accessErr.StatusCode
	}
	return http.StatusInternalServerError
}

func Message(err error) string {
	var accessErr *AccessError
	if errors.As(err, &accessErr) {
		return accessErr.Message
	}
	return http.StatusText(http.StatusInternalServerError)
}

func AllScopes() []string {
	out := make([]string, len(allScopes))
	copy(out, allScopes)
	return out
}

func (a *Auth) writeAccessDenied(w http.ResponseWriter, err error) {
	status := StatusCode(err)
	if a.failureHook != nil {
		a.failureHook(status)
	}
	http.Error(w, Message(err), status)
}

func newPrincipal(name string, scopes []string) Principal {
	cleanScopes := make([]string, 0, len(scopes))
	scopeSet := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, exists := scopeSet[scope]; exists {
			continue
		}
		scopeSet[scope] = struct{}{}
		cleanScopes = append(cleanScopes, scope)
	}
	return Principal{Name: name, Scopes: cleanScopes, scopeSet: scopeSet}
}
