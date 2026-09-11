// Package http 将 system application 的健康状态暴露为只读 HTTP endpoint。
package http

import (
	"net/http"

	"mendry/backend/internal/modules/system/application"
	"mendry/backend/internal/platform/httpserver"
)

// Handler 持有 system application service，并负责健康响应的 HTTP 编解码。
type Handler struct {
	service application.Service
}

// NewHandler 使用显式注入的 service 创建 system HTTP handler。
func NewHandler(service application.Service) Handler {
	return Handler{service: service}
}

// Register 将 liveness、readiness 和版本化 system status route 注册到 mux。
func (h Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /livez", h.liveness)
	mux.HandleFunc("GET /readyz", h.readiness)
	mux.HandleFunc("GET /api/v1/system/status", h.readiness)
}

func (h Handler) liveness(writer http.ResponseWriter, _ *http.Request) {
	_ = httpserver.WriteJSON(writer, http.StatusOK, h.service.Liveness())
}

func (h Handler) readiness(writer http.ResponseWriter, request *http.Request) {
	report := h.service.Readiness(request.Context())
	status := http.StatusOK
	if report.Status != "ready" {
		status = http.StatusServiceUnavailable
	}
	_ = httpserver.WriteJSON(writer, status, report)
}
