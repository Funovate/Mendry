package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"unicode/utf8"

	"mendry/backend/internal/modules/hooks/application"
	"mendry/backend/internal/platform/httpserver"
)

type service interface {
	Ingest(context.Context, string, string) error
}

type contentTypeService interface {
	IngestWithContentType(context.Context, string, string, string) error
}

// HandlerOptions 注入公开 webhook HTTP 适配器依赖。
type HandlerOptions struct {
	Service service
}

// Handler 注册无 Session 的 POST /hooks/{token}。
type Handler struct {
	service service
}

// NewHandler 在注册路由前验证公开 webhook HTTP 依赖。
func NewHandler(options HandlerOptions) (*Handler, error) {
	if options.Service == nil {
		return nil, fmt.Errorf("webhook HTTP dependencies are required")
	}
	return &Handler{service: options.Service}, nil
}

// Register 使用带 {token} 的 mux pattern，让 AccessLog 只记录 pattern 而不打印路径明文。
func (h *Handler) Register(mux *nethttp.ServeMux) {
	mux.HandleFunc("POST /hooks/{token}", h.ingest)
}

type ingestResponse struct {
	Accepted bool `json:"accepted"`
}

func (h *Handler) ingest(writer nethttp.ResponseWriter, request *nethttp.Request) {
	if err := validateContentType(request.Header.Get("Content-Type")); err != nil {
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusBadRequest, Code: "invalid_request", Message: "Webhook request is invalid.",
		})
		return
	}
	raw, err := readInboundBody(request)
	if err != nil {
		writeBodyError(writer, request, err)
		return
	}
	if providerService, ok := h.service.(contentTypeService); ok {
		err = providerService.IngestWithContentType(request.Context(), request.PathValue("token"), raw, request.Header.Get("Content-Type"))
	} else {
		err = h.service.Ingest(request.Context(), request.PathValue("token"), raw)
	}
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	if writeErr := httpserver.WriteJSON(writer, nethttp.StatusAccepted, ingestResponse{Accepted: true}); writeErr != nil {
		httpserver.WriteInternalError(writer, request, writeErr)
	}
}

func validateContentType(value string) error {
	mediaType, _, _ := strings.Cut(value, ";")
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "", "text/plain", "application/json", "application/x-www-form-urlencoded":
		return nil
	default:
		return application.ErrInvalidInput
	}
}

func readInboundBody(request *nethttp.Request) (string, error) {
	if request.Body == nil {
		return "", application.ErrInvalidInput
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(body) {
		return "", application.ErrInvalidInput
	}
	return string(body), nil
}

func writeBodyError(writer nethttp.ResponseWriter, request *nethttp.Request, err error) {
	var maxBytesError *nethttp.MaxBytesError
	if errors.As(err, &maxBytesError) {
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "Request body is too large.",
		})
		return
	}
	if errors.Is(err, application.ErrInvalidInput) {
		writeApplicationError(writer, request, err)
		return
	}
	httpserver.WriteInternalError(writer, request, err)
}

func writeApplicationError(writer nethttp.ResponseWriter, request *nethttp.Request, err error) {
	switch {
	case errors.Is(err, application.ErrAWSSNSTooLarge):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "Request body is too large.",
		})
	case errors.Is(err, application.ErrForbiddenAWSSNS):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusForbidden, Code: "invalid_aws_sns_signature", Message: "AWS SNS signature is invalid.",
		})
	case errors.Is(err, application.ErrTemporaryAWSSNS):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusServiceUnavailable, Code: "aws_sns_unavailable", Message: "AWS SNS verification is temporarily unavailable.",
		})
	case errors.Is(err, application.ErrInvalidAWSSNS):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusBadRequest, Code: "invalid_aws_sns_message", Message: "AWS SNS message is invalid.",
		})
	case errors.Is(err, application.ErrInvalidTencentCallback):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusBadRequest, Code: "invalid_tencent_cls_callback", Message: "Tencent CLS callback is invalid.",
		})
	case errors.Is(err, application.ErrInvalidProbeEvent):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusBadRequest, Code: "invalid_probe_event", Message: "Log probe event is invalid or stale.",
		})
	case errors.Is(err, application.ErrInvalidInput):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusBadRequest, Code: "invalid_request", Message: "Webhook request is invalid.",
		})
	case errors.Is(err, application.ErrWebhookNotFound):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusNotFound, Code: "webhook_not_found", Message: "Webhook was not found.",
		})
	default:
		httpserver.WriteInternalError(writer, request, err)
	}
}
