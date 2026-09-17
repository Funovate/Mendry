package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"
)

// statusClientClosedRequest 遵循常见 reverse proxy 约定，表示客户端在响应完成前关闭请求。
const statusClientClosedRequest = 499

// Error 描述可安全返回给 API 调用方的 HTTP 错误。
type Error struct {
	Status  int
	Code    string
	Message string
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

type successEnvelope struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Data    any          `json:"data"`
	Meta    responseMeta `json:"meta"`
}

type responseMeta struct {
	RequestID  string `json:"requestId"`
	DurationMS int64  `json:"durationMs"`
	Total      *int64 `json:"total,omitempty"`
}

type responseMetadata interface {
	responseMetadata() (string, time.Time)
}

// WriteJSON 在提交 header 前完成编码，避免编码失败产生半截成功响应。
// 成功响应统一使用 code/message/data/meta envelope。
func WriteJSON(writer http.ResponseWriter, status int, value any) error {
	return writeSuccessJSON(writer, status, value, nil)
}

// WriteListJSON 写入带服务端总数的成功列表响应。
func WriteListJSON(writer http.ResponseWriter, status int, value any, total int64) error {
	return writeSuccessJSON(writer, status, value, &total)
}

func writeSuccessJSON(writer http.ResponseWriter, status int, value any, total *int64) error {
	if status == http.StatusNoContent {
		writer.WriteHeader(status)
		return nil
	}
	requestID, started := responseMetadataFor(writer)
	duration := int64(0)
	if !started.IsZero() {
		duration = time.Since(started).Milliseconds()
		if duration < 0 {
			duration = 0
		}
	}
	return writeRawJSON(writer, status, successEnvelope{
		Code: "ok", Message: "OK", Data: value,
		Meta: responseMeta{RequestID: requestID, DurationMS: duration, Total: total},
	})
}

func responseMetadataFor(writer http.ResponseWriter) (string, time.Time) {
	for current := writer; current != nil; {
		if metadata, ok := current.(responseMetadata); ok {
			return metadata.responseMetadata()
		}
		unwrapper, ok := current.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		current = unwrapper.Unwrap()
	}
	return writer.Header().Get(requestIDHeader), time.Time{}
}

func writeRawJSON(writer http.ResponseWriter, status int, value any) error {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(value); err != nil {
		return err
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, err := writer.Write(body.Bytes())
	return err
}

// WriteError 写入稳定错误 envelope，不暴露底层错误或 panic 内容。
func WriteError(writer http.ResponseWriter, request *http.Request, apiError Error) {
	if apiError.Status < 400 || apiError.Status > 599 {
		apiError = Error{Status: http.StatusInternalServerError, Code: "internal_error", Message: "An internal error occurred."}
	}
	_ = writeRawJSON(writer, apiError.Status, errorEnvelope{Error: errorBody{
		Code:      apiError.Code,
		Message:   apiError.Message,
		RequestID: RequestID(request.Context()),
	}})
}

// WriteInternalError 将已取消的 request 记录为客户端关闭；其他非预期服务端 cause
// 仍向客户端写入不含内部细节的稳定 500，并交给 access boundary 记录诊断。
func WriteInternalError(writer http.ResponseWriter, request *http.Request, cause error) {
	if cause == nil {
		cause = errors.New("internal error cause is missing")
	}
	if errors.Is(cause, context.Canceled) && errors.Is(request.Context().Err(), context.Canceled) {
		writer.WriteHeader(statusClientClosedRequest)
		return
	}
	captureInternalError(writer, cause)
	WriteError(writer, request, Error{
		Status: http.StatusInternalServerError, Code: "internal_error", Message: "An internal error occurred.",
	})
}

type internalErrorRecorder interface {
	recordInternalError(error)
}

func captureInternalError(writer http.ResponseWriter, cause error) {
	for current := writer; current != nil; {
		if recorder, ok := current.(internalErrorRecorder); ok {
			recorder.recordInternalError(cause)
			return
		}
		unwrapper, ok := current.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		current = unwrapper.Unwrap()
	}
}

// DecodeJSON 严格解码一个 JSON object：要求正确 media type，拒绝未知字段、
// 多个 JSON value 和超过 middleware 上限的 body。
func DecodeJSON(request *http.Request, destination any) *Error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return &Error{Status: http.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: "Content-Type must be application/json."}
	}

	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return &Error{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "Request body is too large."}
		}
		if errors.Is(err, io.EOF) {
			return &Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Request body must contain one JSON object."}
		}
		return &Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Request body contains invalid JSON."}
	}

	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return &Error{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "Request body is too large."}
		}
		return &Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Request body must contain one JSON object."}
	}
	return nil
}
