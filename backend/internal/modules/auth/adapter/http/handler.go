package http

import (
	"context"
	"errors"
	"net/http"
	"time"

	"mendry/backend/internal/modules/auth/application"
	"mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/platform/httpserver"
)

const (
	SessionCookieName       = "mendry_session"
	legacySessionCookieName = "fixthe_session"
)

type principalContextKey struct{}

type service interface {
	Login(context.Context, string, []byte, string) (application.LoginResult, error)
	Authenticate(context.Context, string) (domain.User, error)
	Logout(context.Context, string) error
}

// HandlerOptions 声明认证 HTTP adapter 的 application 依赖和 cookie policy。
type HandlerOptions struct {
	Service      service
	SecureCookie bool
	Now          func() time.Time
}

// Handler 负责认证 DTO、错误映射和 Session cookie，不实现 credential 规则。
type Handler struct {
	service      service
	secureCookie bool
	now          func() time.Time
}

func NewHandler(options HandlerOptions) (*Handler, error) {
	if options.Service == nil {
		return nil, errors.New("authentication HTTP service is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Handler{service: options.Service, secureCookie: options.SecureCookie, now: options.Now}, nil
}

// Register 注册登录、退出和当前用户 endpoint。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.Handle("GET /api/v1/auth/me", h.RequireAuthentication(http.HandlerFunc(h.me)))
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

func (h *Handler) login(writer http.ResponseWriter, request *http.Request) {
	preventCaching(writer)
	var payload loginRequest
	if decodeError := httpserver.DecodeJSON(request, &payload); decodeError != nil {
		httpserver.WriteError(writer, request, *decodeError)
		return
	}
	password := []byte(payload.Password)
	payload.Password = ""
	defer clear(password)

	result, err := h.service.Login(request.Context(), payload.Username, password, sessionToken(request))
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	h.setSessionCookie(writer, result.Token, result.ExpiresAt)
	if err := httpserver.WriteJSON(writer, http.StatusOK, mapUser(result.User)); err != nil {
		httpserver.WriteInternalError(writer, request, err)
	}
}

func (h *Handler) logout(writer http.ResponseWriter, request *http.Request) {
	preventCaching(writer)
	var payload struct{}
	if decodeError := httpserver.DecodeJSON(request, &payload); decodeError != nil {
		httpserver.WriteError(writer, request, *decodeError)
		return
	}
	if err := h.service.Logout(request.Context(), sessionToken(request)); err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	h.clearSessionCookie(writer)
	writer.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(writer http.ResponseWriter, request *http.Request) {
	preventCaching(writer)
	user, ok := CurrentUser(request.Context())
	if !ok {
		httpserver.WriteError(writer, request, unauthenticatedError())
		return
	}
	if err := httpserver.WriteJSON(writer, http.StatusOK, mapUser(user)); err != nil {
		httpserver.WriteInternalError(writer, request, err)
	}
}

// RequireAuthentication 验证 Session cookie，并把 Principal 放入 request context。
func (h *Handler) RequireAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		preventCaching(writer)
		token := sessionToken(request)
		user, err := h.service.Authenticate(request.Context(), token)
		if err != nil {
			writeApplicationError(writer, request, err)
			return
		}
		ctx := context.WithValue(request.Context(), principalContextKey{}, user)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

// CurrentUser 返回认证 middleware 注入的 Principal。
func CurrentUser(ctx context.Context) (domain.User, bool) {
	user, ok := ctx.Value(principalContextKey{}).(domain.User)
	return user, ok
}

func (h *Handler) setSessionCookie(writer http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(writer, &http.Cookie{
		Name: SessionCookieName, Value: token, Path: "/", Expires: expiresAt.UTC(),
		MaxAge: int(expiresAt.Sub(h.now().UTC()).Seconds()), HttpOnly: true, Secure: h.secureCookie, SameSite: http.SameSiteLaxMode,
	})
	clearCookie(writer, legacySessionCookieName, h.secureCookie)
}

func (h *Handler) clearSessionCookie(writer http.ResponseWriter) {
	clearCookie(writer, SessionCookieName, h.secureCookie)
	clearCookie(writer, legacySessionCookieName, h.secureCookie)
}

func clearCookie(writer http.ResponseWriter, name string, secure bool) {
	http.SetCookie(writer, &http.Cookie{
		Name: name, Value: "", Path: "/", Expires: time.Unix(1, 0).UTC(),
		MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func sessionToken(request *http.Request) string {
	for _, name := range []string{SessionCookieName, legacySessionCookieName} {
		cookie, err := request.Cookie(name)
		if err == nil && cookie.Value != "" {
			return cookie.Value
		}
	}
	return ""
}

func mapUser(user domain.User) userResponse {
	return userResponse{ID: user.ID, Username: user.Username}
}

func preventCaching(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
}

func writeApplicationError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, application.ErrInvalidCredentials):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: http.StatusUnauthorized, Code: "invalid_credentials", Message: "Username or password is invalid.",
		})
	case errors.Is(err, application.ErrUnauthenticated), errors.Is(err, application.ErrSessionNotFound):
		httpserver.WriteError(writer, request, unauthenticatedError())
	case errors.Is(err, application.ErrInvalidInput):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: http.StatusBadRequest, Code: "invalid_request", Message: "The request is invalid.",
		})
	case errors.Is(err, application.ErrUserConflict):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: http.StatusConflict, Code: "user_conflict", Message: "The username is already in use.",
		})
	default:
		httpserver.WriteInternalError(writer, request, err)
	}
}

func unauthenticatedError() httpserver.Error {
	return httpserver.Error{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Authentication is required."}
}
