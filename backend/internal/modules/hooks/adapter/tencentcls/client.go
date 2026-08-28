package tencentcls

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	hooksapplication "fixthe/backend/internal/modules/hooks/application"
	remediationdomain "fixthe/backend/internal/modules/remediation/domain"
	"fixthe/backend/internal/platform/observability"
)

const (
	defaultTimeout          = 10 * time.Second
	defaultMaxRedirects     = 2
	defaultMaxPageBytes     = 256 << 10
	defaultMaxResponseBytes = 4 << 20
	maxRecordIDBytes        = 255
	maxAnalysisItems        = 64
	maxDetailRetryWindow    = 2 * time.Minute
	maxDetailRetryDelays    = 16
)

var (
	ErrDetailUnavailable = errors.New("tencent cls detail unavailable")
	ErrDetailInvalid     = errors.New("tencent cls detail invalid")
	ErrRedirectRejected  = errors.New("tencent cls detail redirect rejected")
	ErrDetailTooLarge    = errors.New("tencent cls detail response too large")
	ErrDetailTimeout     = errors.New("tencent cls detail timed out")

	errTemporaryDetail = errors.New("provider returned temporary error code -1001")
)

var defaultDetailRetryDelays = []time.Duration{
	250 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	30 * time.Second,
}

// ConnectorOutcome 是 connector 对外暴露的低基数结果，用于持久化与审计聚合。
type ConnectorOutcome string

const (
	OutcomeSuccess          ConnectorOutcome = "success"
	OutcomeUnavailable      ConnectorOutcome = "unavailable"
	OutcomeInvalid          ConnectorOutcome = "invalid"
	OutcomeRedirectRejected ConnectorOutcome = "redirect_rejected"
	OutcomeOversized        ConnectorOutcome = "oversized"
	OutcomeTimeout          ConnectorOutcome = "timeout"
)

// ConnectorObservation 保存供持久化与聚合使用的低基数结果；完整请求、响应和
// 原始错误由 adapter 的私有 operator 日志单独记录。
type ConnectorObservation struct {
	Provider       string
	Outcome        ConnectorOutcome
	StatusCode     int
	BytesRetrieved int64
	Contradictions []string
}

// FetchResult 将有界 operational evidence 与 connector observation 分开，避免
// 将 provider 响应正文或传输错误带入持久化边界。
type FetchResult struct {
	Evidence    *OperationalEvidence
	Observation ConnectorObservation
}

// OperationalEvidence 只包含 operator 或后续时间、关联分析所需的 provider
// 字段；原始日志结果区有界保留，但 provider 控制材料不会进入这里。
type OperationalEvidence struct {
	RecordID          string
	AlertID           string
	TopicID           string
	Topic             string
	Logset            string
	Region            string
	Account           string
	OperationalFields map[string]json.RawMessage
	Snapshot          ResultSnapshot
}

// ResultSnapshot 保留 provider 的多维证据区，并由 parseSnapshot 验证
// AnalysisInfo 非空后交给下游投影。
type ResultSnapshot struct {
	AnalysisInfo         []AnalysisInfoItem
	AnalysisResultFormat json.RawMessage
	RawResults           json.RawMessage
	ColNames             json.RawMessage
	Columns              json.RawMessage
	QueryParams          json.RawMessage
}

// AnalysisInfoItem 是单个已配置 analysis 的有界投影；原始结果字段保持完整，
// provider 控制 envelope 只进入明确允许的字段。
type AnalysisInfoItem struct {
	Name            string
	Type            string
	Fields          string
	QueryIndex      int64
	Limit           int64
	Configuration   json.RawMessage
	ColNames        json.RawMessage
	Columns         json.RawMessage
	RawResult       json.RawMessage
	FormattedResult json.RawMessage
	Error           *AnalysisItemError
}

// AnalysisItemError 表示 provider 对单个 analysis 返回的安全错误摘要。
type AnalysisItemError struct {
	Code    string
	Message string
}

// ConnectorError 保留稳定分类、失败阶段和原始 adapter cause。Error 与 Unwrap
// 维持安全的 application 边界，只有私有 operator 日志读取 Cause 完整诊断。
type ConnectorError struct {
	Code      string
	Stage     string
	Retryable bool
	Kind      error
	Cause     error
}

func (e *ConnectorError) Error() string {
	if e == nil {
		return "tencent cls detail connector error"
	}
	detail := e.Kind
	if detail != nil && e.Stage != "" {
		return e.Stage + ": " + detail.Error()
	}
	if detail != nil {
		return detail.Error()
	}
	if e.Code != "" {
		return e.Code
	}
	return "tencent cls detail connector error"
}

func (e *ConnectorError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

func connectorError(outcome ConnectorOutcome, stage string, retryable bool, kind, cause error) *ConnectorError {
	return &ConnectorError{
		Code: string(outcome), Stage: stage, Retryable: retryable,
		Kind: kind, Cause: cause,
	}
}

// Options 限制受信任的 Tencent CLS connector。HTTPClient 会在复制后由本构造器
// 替换 timeout 与 redirect policy，调用方传入的 client 不会被改写。
type Options struct {
	HTTPClient       *http.Client
	Logger           *slog.Logger
	Timeout          time.Duration
	MaxRedirects     int
	MaxPageBytes     int64
	MaxResponseBytes int64
	// RetryDelays 只控制 provider 返回临时 -1001 后的重试。nil 使用有界生产
	// schedule；测试可注入零 delay 来覆盖同一重试路径而不等待生产时长。
	RetryDelays []time.Duration
}

// Client 解析 Tencent 的匿名 alert detail page，并调用固定的 detail API action。
type Client struct {
	httpClient       *http.Client
	logger           *slog.Logger
	timeout          time.Duration
	maxRedirects     int
	maxPageBytes     int64
	maxResponseBytes int64
	retryDelays      []time.Duration
}

// NewClient 校验 connector 的 timeout、redirect、响应大小与 retry schedule，并
// 构造不会修改调用方 HTTPClient 的受信任 Tencent CLS client。
func NewClient(options Options) (*Client, error) {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if timeout > 2*time.Minute {
		return nil, fmt.Errorf("tencent CLS detail timeout is too large")
	}
	redirects := options.MaxRedirects
	if redirects <= 0 {
		redirects = defaultMaxRedirects
	}
	if redirects > 4 {
		return nil, fmt.Errorf("tencent CLS detail redirect limit is too large")
	}
	pageBytes := options.MaxPageBytes
	if pageBytes <= 0 {
		pageBytes = defaultMaxPageBytes
	}
	if pageBytes > 2<<20 {
		return nil, fmt.Errorf("tencent CLS detail page limit is too large")
	}
	responseBytes := options.MaxResponseBytes
	if responseBytes <= 0 {
		responseBytes = defaultMaxResponseBytes
	}
	if responseBytes > 16<<20 {
		return nil, fmt.Errorf("tencent CLS detail response limit is too large")
	}
	retryDelays, err := validateRetryDelays(options.RetryDelays)
	if err != nil {
		return nil, err
	}

	base := http.Client{}
	if options.HTTPClient != nil {
		base = *options.HTTPClient
	}
	base.Timeout = timeout
	base.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= redirects || request.URL == nil || !hooksapplication.IsValidTencentDetailURL(request.URL.String()) {
			return ErrRedirectRejected
		}
		return nil
	}
	return &Client{
		httpClient:       &base,
		logger:           options.Logger,
		timeout:          timeout,
		maxRedirects:     redirects,
		maxPageBytes:     pageBytes,
		maxResponseBytes: responseBytes,
		retryDelays:      retryDelays,
	}, nil
}

// Resolve 根据已校验 callback 获取 Tencent detail record；失败同时返回安全的
// connector observation，供调用方记录已接受 webhook 后的降级状态。
func (c *Client) Resolve(ctx context.Context, callback hooksapplication.TencentCLSCallback) (result FetchResult, returnErr error) {
	started := time.Now()
	defer func() {
		c.logResolution(ctx, callback.TopicID, result, returnErr, time.Since(started))
	}()
	if c == nil || c.httpClient == nil || !hooksapplication.IsValidTencentDetailURL(callback.DetailURL) {
		err := connectorError(OutcomeInvalid, "validate_callback", false, ErrDetailInvalid, ErrDetailInvalid)
		return failed(OutcomeInvalid, err, false, 0, 0), err
	}
	pageResponse, pageBody, err := c.fetch(ctx, "detail_page", http.MethodGet, callback.DetailURL, "text/html", nil, c.maxPageBytes)
	if err != nil {
		result := c.mapFailure(err)
		observeFetch(&result.Observation, pageResponse, pageBody)
		return result, err
	}
	pageURL := callback.DetailURL
	if pageResponse.Request != nil && pageResponse.Request.URL != nil {
		pageURL = pageResponse.Request.URL.String()
	}
	recordID, err := extractRecordID(pageBody, pageURL)
	if err != nil {
		connectorErr := connectorError(OutcomeInvalid, "extract_record_id", false, ErrDetailInvalid, err)
		return failed(OutcomeInvalid, connectorErr, false, pageResponse.StatusCode, int64(len(pageBody))), connectorErr
	}

	apiURL, err := fixedDetailAPIURL(pageURL)
	if err != nil {
		connectorErr := connectorError(OutcomeInvalid, "build_detail_api_url", false, ErrDetailInvalid, err)
		return failed(OutcomeInvalid, connectorErr, false, pageResponse.StatusCode, int64(len(pageBody))), connectorErr
	}
	body, err := json.Marshal(struct {
		RecordID string `json:"RecordId"`
	}{RecordID: recordID})
	if err != nil {
		connectorErr := connectorError(OutcomeInvalid, "encode_detail_request", false, ErrDetailInvalid, err)
		return failed(OutcomeInvalid, connectorErr, false, pageResponse.StatusCode, int64(len(pageBody))), connectorErr
	}
	detailResponse, detailBody, detailBytes, err := c.fetchDetailWithRetry(ctx, apiURL, body)
	if err != nil {
		result := c.mapFailure(err)
		result.Observation.StatusCode = responseStatus(detailResponse)
		result.Observation.BytesRetrieved += int64(len(pageBody)) + detailBytes
		return result, err
	}
	evidence, err := parseDetailResponse(detailBody, recordID)
	if err != nil {
		connectorErr := connectorError(OutcomeInvalid, "parse_detail_response", false, ErrDetailInvalid, err)
		return failed(OutcomeInvalid, connectorErr, false, detailResponse.StatusCode, int64(len(pageBody))+detailBytes), connectorErr
	}
	observation := ConnectorObservation{
		Provider:       "tencent_cls",
		Outcome:        OutcomeSuccess,
		StatusCode:     detailResponse.StatusCode,
		BytesRetrieved: int64(len(pageBody)) + detailBytes,
	}
	if callback.TopicID != "" && evidence.TopicID != "" && callback.TopicID != evidence.TopicID {
		observation.Contradictions = []string{"tencent_topic_id_mismatch"}
	}
	return FetchResult{Evidence: &evidence, Observation: observation}, nil
}

// ResolveEvidenceObserved 解析已校验 callback，并返回安全 evidence projection、
// connector observation 与有界 adapter error。
func (c *Client) ResolveEvidenceObserved(ctx context.Context, callback hooksapplication.TencentCLSCallback) (hooksapplication.ProviderDetailEvidence, ConnectorObservation, error) {
	result, err := c.Resolve(ctx, callback)
	projection := projectEvidence(callback, result)
	c.logEvidenceProjection(ctx, callback.TopicID, projection)
	return projection, result.Observation, err
}

// ResolveEvidence 将 CLS connector 的完整运行证据投影为 hooks application 可持久化
// 的安全值。失败只返回有界 connector observation，不把 URL、响应 body 或 transport
// error 传播到 evidence port。
func (c *Client) ResolveEvidence(ctx context.Context, callback hooksapplication.TencentCLSCallback) hooksapplication.ProviderDetailEvidence {
	projection, _, _ := c.ResolveEvidenceObserved(ctx, callback)
	return projection
}

func projectEvidence(callback hooksapplication.TencentCLSCallback, result FetchResult) hooksapplication.ProviderDetailEvidence {
	if result.Evidence != nil {
		payload, err := json.Marshal(result.Evidence)
		if err != nil {
			return unavailableProjection(result.Observation, callback.TopicID, "encode_operational_evidence")
		}
		classification := remediationdomain.EvidenceContextual
		primary := false
		temporal := false
		operational := false
		if hasDirectAnalysis(*result.Evidence) && len(result.Observation.Contradictions) == 0 {
			classification = remediationdomain.EvidenceDirectFault
			primary = true
			temporal = true
			operational = true
		}
		if len(result.Observation.Contradictions) > 0 {
			classification = remediationdomain.EvidenceContradictory
		}
		contradictions := result.Observation.Contradictions
		if contradictions == nil {
			contradictions = []string{}
		}
		provenance, _ := json.Marshal(map[string]any{
			"adapter":                     "tencent_cls",
			"record_id":                   result.Evidence.RecordID,
			"status_code":                 result.Observation.StatusCode,
			"bytes_retrieved":             result.Observation.BytesRetrieved,
			"contradictions":              contradictions,
			"detail_capability_validated": true,
			"detail_resolution":           "validated_provider_detail_get_alert_detail",
		})
		return hooksapplication.ProviderDetailEvidence{
			Provider:               "tencent_cls",
			EvidenceKind:           remediationdomain.EvidenceKindProviderDetail,
			DeduplicationKey:       "record:" + result.Evidence.RecordID,
			Classification:         classification,
			Outcome:                string(OutcomeSuccess),
			Available:              true,
			Primary:                primary,
			TemporalCorrelation:    temporal,
			OperationalCorrelation: operational,
			Payload:                payload,
			Provenance:             provenance,
			ByteCount:              int64(len(payload)),
		}
	}
	return unavailableProjection(result.Observation, callback.TopicID, "detail_unavailable")
}

func observeFetch(observation *ConnectorObservation, response *http.Response, body []byte) {
	if observation == nil {
		return
	}
	if response != nil {
		observation.StatusCode = response.StatusCode
	}
	observation.BytesRetrieved += int64(len(body))
}

func (c *Client) logResolution(ctx context.Context, topicID string, result FetchResult, cause error, duration time.Duration) {
	if c == nil || c.logger == nil {
		return
	}
	observation := result.Observation
	outcome := observation.Outcome
	if outcome == "" {
		outcome = OutcomeUnavailable
	}
	level := slog.LevelInfo
	attrs := []slog.Attr{
		slog.String(observability.FieldComponent, "hooks"),
		slog.String(observability.FieldProvider, "tencent_cls"),
		slog.String(observability.FieldTopicID, topicID),
		slog.String(observability.FieldOutcome, string(outcome)),
		slog.Int(observability.FieldStatusCode, observation.StatusCode),
		slog.Int64(observability.FieldBytesRetrieved, observation.BytesRetrieved),
		slog.Int64(observability.FieldDurationMS, duration.Milliseconds()),
	}
	if cause != nil || outcome != OutcomeSuccess {
		level = slog.LevelWarn
		retryable := false
		errorCode := string(outcome)
		errorType := "connector"
		errorMessage := errorCode
		if cause != nil {
			errorType = fmt.Sprintf("%T", cause)
			errorMessage = sanitizeTencentLogText(cause.Error())
		}
		var connectorErr *ConnectorError
		if errors.As(cause, &connectorErr) && connectorErr != nil {
			errorCode = connectorErr.Code
			retryable = connectorErr.Retryable
			if connectorErr.Cause != nil {
				errorType = fmt.Sprintf("%T", connectorErr.Cause)
				errorMessage = connectorErr.Stage + ": " + sanitizeTencentLogText(connectorErr.Cause.Error())
			}
		}
		attrs = append(attrs,
			slog.String(observability.FieldErrorClass, "connector"),
			slog.String(observability.FieldErrorCode, errorCode),
			slog.String(observability.FieldErrorType, errorType),
			slog.String(observability.FieldErrorMessage, errorMessage),
			slog.Bool(observability.FieldRetryable, retryable),
		)
		if connectorErr != nil && connectorErr.Stage != "" {
			attrs = append(attrs, slog.String(observability.FieldErrorStage, connectorErr.Stage))
		}
	}
	observability.Log(ctx, c.logger, level, observability.EventTencentCLSDetailCompleted, "Tencent CLS detail resolution completed", attrs...)
}

func (c *Client) logEvidenceProjection(ctx context.Context, topicID string, evidence hooksapplication.ProviderDetailEvidence) {
	if c == nil || c.logger == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String(observability.FieldComponent, "hooks"),
		slog.String(observability.FieldProvider, "tencent_cls"),
		slog.String(observability.FieldTopicID, topicID),
		slog.String(observability.FieldOutcome, evidence.Outcome),
		slog.Bool(observability.FieldAvailable, evidence.Available),
		slog.String(observability.FieldPayloadKind, evidence.EvidenceKind),
		slog.Int(observability.FieldPayloadBytes, len(evidence.Payload)),
	}
	if len(evidence.Payload) > 0 {
		attrs = append(attrs, slog.String(observability.FieldPayload, string(evidence.Payload)))
	}
	observability.Log(ctx, c.logger, slog.LevelInfo, observability.EventTencentCLSEvidenceProjected, "Tencent CLS evidence projected for remediation", attrs...)
}

func unavailableProjection(observation ConnectorObservation, topicID, fallbackReason string) hooksapplication.ProviderDetailEvidence {
	outcome := string(observation.Outcome)
	if outcome == "" {
		outcome = string(OutcomeUnavailable)
	}
	reason := outcome
	if reason == "" {
		reason = fallbackReason
	}
	payload, _ := json.Marshal(map[string]any{
		"provider":        "tencent_cls",
		"available":       false,
		"outcome":         outcome,
		"status_code":     observation.StatusCode,
		"bytes_retrieved": observation.BytesRetrieved,
		"reason":          reason,
	})
	provenance, _ := json.Marshal(map[string]any{
		"adapter":         "tencent_cls",
		"outcome":         outcome,
		"topic_id":        topicID,
		"status_code":     observation.StatusCode,
		"bytes_retrieved": observation.BytesRetrieved,
		"contradictions":  observation.Contradictions,
	})
	classification := remediationdomain.EvidenceContextual
	if len(observation.Contradictions) > 0 {
		classification = remediationdomain.EvidenceContradictory
	}
	return hooksapplication.ProviderDetailEvidence{
		Provider:         "tencent_cls",
		EvidenceKind:     remediationdomain.EvidenceKindConnectorObservation,
		DeduplicationKey: "topic:" + topicID + ":detail-connector",
		Classification:   classification,
		Outcome:          outcome,
		Available:        false,
		Payload:          payload,
		Provenance:       provenance,
		ByteCount:        int64(len(payload)),
	}
}

func hasDirectAnalysis(evidence OperationalEvidence) bool {
	if len(bytes.TrimSpace(evidence.Snapshot.RawResults)) == 0 || string(bytes.TrimSpace(evidence.Snapshot.RawResults)) == "null" || string(bytes.TrimSpace(evidence.Snapshot.RawResults)) == "[]" {
		return false
	}
	for _, item := range evidence.Snapshot.AnalysisInfo {
		if strings.EqualFold(strings.TrimSpace(item.Type), "original") {
			return true
		}
	}
	return false
}

func validateRetryDelays(delays []time.Duration) ([]time.Duration, error) {
	if len(delays) == 0 {
		return append([]time.Duration(nil), defaultDetailRetryDelays...), nil
	}
	if len(delays) > maxDetailRetryDelays {
		return nil, fmt.Errorf("tencent CLS detail retry schedule is too long")
	}
	var total time.Duration
	for _, delay := range delays {
		if delay < 0 {
			return nil, fmt.Errorf("tencent CLS detail retry delay is invalid")
		}
		if delay >= maxDetailRetryWindow-total {
			return nil, fmt.Errorf("tencent CLS detail retry window is too large")
		}
		total += delay
	}
	return append([]time.Duration(nil), delays...), nil
}

func (c *Client) fetchDetailWithRetry(ctx context.Context, target string, body []byte) (response *http.Response, data []byte, bytesRetrieved int64, returnErr error) {
	for attempt := 0; ; attempt++ {
		response, data, returnErr = c.fetch(ctx, "get_alert_detail", http.MethodPost, target, "application/json", body, c.maxResponseBytes)
		bytesRetrieved += int64(len(data))
		if returnErr != nil || !isTemporaryDetailResponse(data) {
			return response, data, bytesRetrieved, returnErr
		}
		if attempt >= len(c.retryDelays) {
			return response, data, bytesRetrieved, connectorError(
				OutcomeUnavailable, "get_alert_detail.eventual_consistency", true,
				ErrDetailUnavailable, errTemporaryDetail,
			)
		}
		if err := waitForRetry(ctx, c.retryDelays[attempt]); err != nil {
			return response, data, bytesRetrieved, connectorError(
				OutcomeUnavailable, "get_alert_detail.retry_wait", true,
				ErrDetailUnavailable, err,
			)
		}
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func responseStatus(response *http.Response) int {
	if response == nil {
		return 0
	}
	return response.StatusCode
}

func isTemporaryDetailResponse(body []byte) bool {
	root, err := decodeObject(body)
	if err != nil {
		return false
	}
	responseRaw, ok := rawByName(root, "Response")
	if !ok {
		return false
	}
	response, err := decodeObject(responseRaw)
	if err != nil {
		return false
	}
	errorRaw, ok := rawByName(response, "Error")
	if !ok {
		return false
	}
	errorObject, err := decodeObject(errorRaw)
	if err != nil {
		return false
	}
	codeRaw, ok := rawByName(errorObject, "Code")
	if !ok {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(codeRaw))
	decoder.UseNumber()
	var code any
	if decoder.Decode(&code) == nil {
		switch value := code.(type) {
		case json.Number:
			return value.String() == "-1001"
		case string:
			return strings.TrimSpace(value) == "-1001"
		}
	}
	return false
}

func (c *Client) fetch(ctx context.Context, operation, method, target, expectedMediaType string, body []byte, maxBytes int64) (response *http.Response, data []byte, returnErr error) {
	started := time.Now()
	defer func() {
		c.logRequest(ctx, operation, method, target, body, response, data, returnErr, time.Since(started))
	}()
	requestContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, nil, connectorError(OutcomeInvalid, operation+".build_request", false, ErrDetailInvalid, err)
	}
	request.Header.Set("Accept", expectedMediaType)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err = c.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, ErrRedirectRejected) {
			return response, nil, connectorError(OutcomeRedirectRejected, operation+".redirect", false, ErrRedirectRejected, err)
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return response, nil, connectorError(OutcomeTimeout, operation+".send", true, ErrDetailTimeout, err)
		}
		return response, nil, connectorError(OutcomeUnavailable, operation+".send", true, ErrDetailUnavailable, err)
	}
	defer response.Body.Close()
	data, err = io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return response, data, connectorError(OutcomeUnavailable, operation+".read_response", true, ErrDetailUnavailable, err)
	}
	if response.ContentLength > maxBytes || int64(len(data)) > maxBytes {
		cause := fmt.Errorf("response exceeded %d-byte limit", maxBytes)
		return response, data, connectorError(OutcomeOversized, operation+".response_size", false, ErrDetailTooLarge, cause)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		cause := fmt.Errorf("unexpected HTTP status %s", response.Status)
		return response, data, connectorError(OutcomeUnavailable, operation+".http_status", response.StatusCode >= 500, ErrDetailUnavailable, cause)
	}
	if response.Request == nil || response.Request.URL == nil || !hooksapplication.IsValidTencentDetailURL(response.Request.URL.String()) {
		return response, data, connectorError(OutcomeRedirectRejected, operation+".final_url", false, ErrRedirectRejected, ErrRedirectRejected)
	}
	actualContentType := response.Header.Get("Content-Type")
	mediaType, _, mediaErr := mime.ParseMediaType(actualContentType)
	if mediaErr != nil {
		cause := fmt.Errorf("parse response Content-Type %q: %w", actualContentType, mediaErr)
		return response, data, connectorError(OutcomeInvalid, operation+".response_media_type", false, ErrDetailInvalid, cause)
	}
	if !acceptsResponseMediaType(operation, mediaType, expectedMediaType) {
		cause := fmt.Errorf("expected response media type %q, got %q", expectedMediaType, mediaType)
		return response, data, connectorError(OutcomeInvalid, operation+".response_media_type", false, ErrDetailInvalid, cause)
	}
	return response, data, nil
}

func acceptsResponseMediaType(operation, actual, expected string) bool {
	if strings.EqualFold(actual, expected) {
		return true
	}
	return operation == "get_alert_detail" && strings.EqualFold(actual, "text/plain")
}

func (c *Client) logRequest(ctx context.Context, operation, method, target string, requestBody []byte, response *http.Response, responseBody []byte, cause error, duration time.Duration) {
	if c == nil || c.logger == nil {
		return
	}
	level := slog.LevelInfo
	outcome := "success"
	attrs := []slog.Attr{
		slog.String(observability.FieldComponent, "hooks"),
		slog.String(observability.FieldProvider, "tencent_cls"),
		slog.String(observability.FieldOperation, operation),
		slog.String(observability.FieldHTTPMethod, method),
		slog.String(observability.FieldHTTPURL, sanitizeTencentRequestURL(target)),
		slog.Int64(observability.FieldDurationMS, duration.Milliseconds()),
		slog.Int64(observability.FieldBytesRetrieved, int64(len(responseBody))),
	}
	if len(requestBody) > 0 {
		attrs = append(attrs, slog.String(observability.FieldHTTPRequest, string(requestBody)))
	}
	if response != nil {
		attrs = append(attrs, slog.Int(observability.FieldHTTPStatus, response.StatusCode))
		if response.Request != nil && response.Request.URL != nil && response.Request.URL.String() != target {
			attrs = append(attrs, slog.String(observability.FieldHTTPFinalURL, sanitizeTencentRequestURL(response.Request.URL.String())))
		}
	}
	if len(responseBody) > 0 {
		attrs = append(attrs, slog.String(observability.FieldHTTPResponse, sanitizeTencentResponseBody(operation, responseBody)))
	}
	if cause != nil {
		level = slog.LevelWarn
		outcome = "failure"
		errorType := fmt.Sprintf("%T", cause)
		errorMessage := sanitizeTencentLogText(cause.Error())
		var connectorErr *ConnectorError
		if errors.As(cause, &connectorErr) && connectorErr != nil && connectorErr.Cause != nil {
			errorType = fmt.Sprintf("%T", connectorErr.Cause)
			errorMessage = connectorErr.Stage + ": " + sanitizeTencentLogText(connectorErr.Cause.Error())
		}
		attrs = append(attrs,
			slog.String(observability.FieldErrorType, errorType),
			slog.String(observability.FieldErrorMessage, errorMessage),
		)
		if errors.As(cause, &connectorErr) && connectorErr != nil {
			attrs = append(attrs,
				slog.String(observability.FieldErrorCode, connectorErr.Code),
				slog.String(observability.FieldErrorStage, connectorErr.Stage),
				slog.Bool(observability.FieldRetryable, connectorErr.Retryable),
			)
			if connectorErr.Code == string(OutcomeOversized) && len(responseBody) > 0 {
				attrs = append(attrs, slog.Bool(observability.FieldHTTPResponseTruncated, true))
			}
		}
	}
	attrs = append(attrs, slog.String(observability.FieldOutcome, outcome))
	observability.Log(ctx, c.logger, level, observability.EventTencentCLSRequestCompleted, "Tencent CLS request completed", attrs...)
}

var tencentLogURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

// normalizeTencentControlMarker 去除分隔符与括号后统一 control marker，覆盖 HTML/JavaScript 常见属性写法。
func normalizeTencentControlMarker(value string) string {
	var normalized strings.Builder
	normalized.Grow(len(value))
	for _, char := range strings.ToLower(value) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			normalized.WriteByte(byte(char))
		}
	}
	return normalized.String()
}

func hasTencentControlMarker(value string) bool {
	normalized := normalizeTencentControlMarker(value)
	for _, marker := range []string{"callback", "webhook", "h5alarmshield", "secretid", "secrettext"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func sanitizeTencentRequestURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "[redacted-url]"
	}
	parsed.User = nil
	parsed.Fragment = ""
	parsed.RawFragment = ""
	parsed.RawPath = ""
	parsed.ForceQuery = false
	if parsed.Path == "/cls_no_login" {
		action := parsed.Query().Get("action")
		parsed.RawQuery = ""
		if action == "GetAlertDetail" || action == "GetAlertDetailPage" {
			parsed.RawQuery = url.Values{"action": {action}}.Encode()
		}
	} else {
		parsed.Path = "/"
		parsed.RawQuery = ""
	}
	return parsed.String()
}

func sanitizeTencentLogText(value string) string {
	return tencentLogURLPattern.ReplaceAllStringFunc(value, sanitizeTencentRequestURL)
}

func sanitizeTencentResponseBody(operation string, raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if operation == "detail_page" {
		return sanitizeTencentHTMLResponseBody(raw)
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		redacted, err := json.Marshal(redactTencentControlMaterial(value))
		if err == nil {
			return sanitizeTencentLogText(string(redacted))
		}
		return fmt.Sprintf("[%s response body omitted: redaction failed]", operation)
	}
	if operation == "get_alert_detail" {
		return fmt.Sprintf("[%s response body omitted: invalid JSON, bytes=%d]", operation, len(raw))
	}
	return sanitizeTencentLogText(string(raw))
}

func sanitizeTencentHTMLResponseBody(raw []byte) string {
	if hasTencentControlMarker(string(raw)) {
		return fmt.Sprintf("[detail_page response body omitted: control marker detected, bytes=%d]", len(raw))
	}
	return sanitizeTencentLogText(string(raw))
}

func redactTencentControlMaterial(value any) any {
	switch current := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(current))
		for key, child := range current {
			if isTencentControlKey(key) {
				continue
			}
			redacted[key] = redactTencentControlMaterial(child)
		}
		return redacted
	case []any:
		redacted := make([]any, len(current))
		for index, child := range current {
			redacted[index] = redactTencentControlMaterial(child)
		}
		return redacted
	case string:
		return sanitizeTencentControlText(current)
	default:
		return value
	}
}

func sanitizeTencentControlText(value string) string {
	if hasTencentControlMarker(value) {
		return "[redacted]"
	}
	return sanitizeTencentLogText(value)
}

func isTencentControlKey(key string) bool {
	normalized := normalizeTencentControlMarker(key)
	for _, marker := range []string{"secret", "callback", "webhook", "h5alarmshield"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func (c *Client) mapFailure(err error) FetchResult {
	var connectorErr *ConnectorError
	if !errors.As(err, &connectorErr) {
		connectorErr = connectorError(OutcomeUnavailable, "unknown", true, ErrDetailUnavailable, err)
	}
	outcome := ConnectorOutcome(connectorErr.Code)
	return failed(outcome, connectorErr, connectorErr.Retryable, 0, 0)
}

func failed(outcome ConnectorOutcome, err error, _ bool, status int, bytesRetrieved int64) FetchResult {
	return FetchResult{Observation: ConnectorObservation{
		Provider:       "tencent_cls",
		Outcome:        outcome,
		StatusCode:     status,
		BytesRetrieved: bytesRetrieved,
	}}
}

func fixedDetailAPIURL(pageURL string) (string, error) {
	parsed, err := url.Parse(pageURL)
	if err != nil || !hooksapplication.IsValidTencentDetailURL(pageURL) {
		return "", ErrDetailInvalid
	}
	parsed.Path = "/cls_no_login"
	parsed.RawPath = ""
	parsed.RawQuery = "action=GetAlertDetail"
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String(), nil
}

var recordIDPattern = regexp.MustCompile(`(?i)(?:["']?recordid["']?)\s*[:=]\s*["']([^"']+)["']`)

func extractRecordID(page []byte, detailURL string) (string, error) {
	if value := recordIDFromDetailURL(detailURL); validRecordID(value) {
		return value, nil
	}
	var document interface{}
	decoder := json.NewDecoder(bytes.NewReader(page))
	if decoder.Decode(&document) == nil {
		if value := findString(document, "recordid", 0); validRecordID(value) {
			return value, nil
		}
	}
	if match := recordIDPattern.FindSubmatch(page); len(match) == 2 {
		value := strings.TrimSpace(string(match[1]))
		if validRecordID(value) {
			return value, nil
		}
	}
	parsed, err := url.Parse(detailURL)
	if err == nil {
		for _, key := range []string{"RecordId", "recordId", "record_id"} {
			if value := strings.TrimSpace(parsed.Query().Get(key)); validRecordID(value) {
				return value, nil
			}
		}
	}
	return "", fmt.Errorf("%w: RecordId was not found in page content or detail URL route parameters", ErrDetailInvalid)
}

func recordIDFromDetailURL(detailURL string) string {
	parsed, err := url.Parse(detailURL)
	if err != nil {
		return ""
	}
	if fragment, err := url.Parse(strings.TrimPrefix(parsed.Fragment, "#")); err == nil {
		for _, key := range []string{"RecordId", "recordId", "record_id"} {
			if value := strings.TrimSpace(fragment.Query().Get(key)); validRecordID(value) {
				return value
			}
		}
	}
	for _, key := range []string{"RecordId", "recordId", "record_id"} {
		if value := strings.TrimSpace(parsed.Query().Get(key)); validRecordID(value) {
			return value
		}
	}
	return ""
}

func validRecordID(value string) bool {
	if value == "" || len(value) > maxRecordIDBytes {
		return false
	}
	for _, r := range value {
		if r == '/' || r == '\\' || r == '?' || r == '#' || r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return false
		}
	}
	return true
}

func findString(value interface{}, key string, depth int) string {
	if depth > 8 {
		return ""
	}
	switch current := value.(type) {
	case map[string]interface{}:
		for name, item := range current {
			if strings.EqualFold(name, key) {
				if result, ok := item.(string); ok {
					return strings.TrimSpace(result)
				}
			}
			if result := findString(item, key, depth+1); result != "" {
				return result
			}
		}
	case []interface{}:
		for _, item := range current {
			if result := findString(item, key, depth+1); result != "" {
				return result
			}
		}
	}
	return ""
}

func parseDetailResponse(body []byte, fallbackRecordID string) (OperationalEvidence, error) {
	root, err := decodeObject(body)
	if err != nil {
		return OperationalEvidence{}, fmt.Errorf("decode detail response: %w", err)
	}
	root, err = unwrapDetailRecord(root)
	if err != nil {
		return OperationalEvidence{}, err
	}
	snapshotRaw, snapshotParent, ok := findObjectByKey(root, "ResultsSnapshot", 0)
	if !ok {
		if _, hasAnalysis := rawByName(root, "AnalysisInfo"); hasAnalysis {
			snapshotRaw, snapshotParent, ok = root, root, true
		}
	}
	if !ok {
		return OperationalEvidence{}, fmt.Errorf("%w: ResultsSnapshot or AnalysisInfo is missing", ErrDetailInvalid)
	}
	snapshot, err := parseSnapshot(snapshotRaw)
	if err != nil {
		return OperationalEvidence{}, fmt.Errorf("parse ResultsSnapshot: %w", err)
	}
	scopes := []map[string]json.RawMessage{snapshotParent, snapshotRaw, root}
	recordID := firstString(scopes, "RecordId", "RecordID", "record_id")
	if !validRecordID(recordID) {
		recordID = fallbackRecordID
	}
	if !validRecordID(recordID) {
		return OperationalEvidence{}, fmt.Errorf("%w: detail response RecordId is missing or invalid", ErrDetailInvalid)
	}
	evidence := OperationalEvidence{
		RecordID: recordID, AlertID: firstString(scopes, "AlertId", "AlertID", "AlarmId"),
		TopicID: firstString(scopes, "TopicId", "TopicID"), Topic: firstString(scopes, "Topic", "TopicName"),
		Logset: firstString(scopes, "Logset", "LogsetName", "LogsetId"), Region: firstString(scopes, "Region"),
		Account: firstString(scopes, "UIN", "Account", "AccountId"), Snapshot: snapshot,
		OperationalFields: selectOperationalFields(scopes),
	}
	return evidence, nil
}

func unwrapDetailRecord(root map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	responseRaw, hasResponse := rawByName(root, "Response")
	if hasResponse {
		response, err := decodeObject(responseRaw)
		if err != nil {
			return nil, fmt.Errorf("%w: decode Response: %v", ErrDetailInvalid, err)
		}
		recordRaw, ok := rawByName(response, "Record")
		if !ok {
			return nil, fmt.Errorf("%w: Response.Record is missing", ErrDetailInvalid)
		}
		record, err := decodeObject(recordRaw)
		if err != nil {
			return nil, fmt.Errorf("%w: decode Response.Record: %v", ErrDetailInvalid, err)
		}
		return record, nil
	}
	if dataRaw, hasData := rawByName(root, "data"); hasData {
		data, err := decodeObject(dataRaw)
		if err != nil {
			return nil, fmt.Errorf("%w: decode data: %v", ErrDetailInvalid, err)
		}
		return data, nil
	}
	return root, nil
}

func decodeObject(body []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value map[string]json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: decode JSON object: %v", ErrDetailInvalid, err)
	}
	if value == nil {
		return nil, fmt.Errorf("%w: expected a JSON object", ErrDetailInvalid)
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return nil, fmt.Errorf("%w: response contains multiple JSON values", ErrDetailInvalid)
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: decode trailing JSON data: %v", ErrDetailInvalid, err)
	}
	return value, nil
}

func parseSnapshot(raw map[string]json.RawMessage) (ResultSnapshot, error) {
	analysisRaw, ok := rawByName(raw, "AnalysisInfo")
	if !ok {
		return ResultSnapshot{}, fmt.Errorf("%w: AnalysisInfo is missing", ErrDetailInvalid)
	}
	analysis, err := parseAnalysisInfo(analysisRaw)
	if err != nil {
		return ResultSnapshot{}, err
	}
	if len(analysis) == 0 {
		return ResultSnapshot{}, fmt.Errorf("%w: AnalysisInfo is empty", ErrDetailInvalid)
	}
	if len(analysis) > maxAnalysisItems {
		return ResultSnapshot{}, fmt.Errorf("%w: AnalysisInfo has %d items, limit is %d", ErrDetailInvalid, len(analysis), maxAnalysisItems)
	}
	return ResultSnapshot{
		AnalysisInfo:         analysis,
		AnalysisResultFormat: cloneRaw(rawByNameOrNil(raw, "AnalysisResultFormat")),
		RawResults:           cloneRaw(rawByNameOrNil(raw, "RawResults")),
		ColNames:             cloneRaw(rawByNameOrNil(raw, "ColNames")),
		Columns:              cloneRaw(rawByNameOrNil(raw, "Columns")),
		QueryParams:          cloneRaw(rawByNameOrNil(raw, "QueryParams")),
	}, nil
}

func parseAnalysisInfo(raw json.RawMessage) ([]AnalysisInfoItem, error) {
	var entries []json.RawMessage
	if arrayErr := json.Unmarshal(raw, &entries); arrayErr != nil {
		var keyed map[string]json.RawMessage
		if mapErr := json.Unmarshal(raw, &keyed); mapErr != nil {
			return nil, fmt.Errorf("%w: AnalysisInfo is neither an array nor object: array=%v object=%v", ErrDetailInvalid, arrayErr, mapErr)
		}
		entries = make([]json.RawMessage, 0, len(keyed))
		for _, value := range keyed {
			entries = append(entries, value)
		}
	}
	items := make([]AnalysisInfoItem, 0, len(entries))
	for _, entry := range entries {
		fields, err := decodeObject(entry)
		if err != nil {
			return nil, fmt.Errorf("%w: decode AnalysisInfo item %d: %v", ErrDetailInvalid, len(items), err)
		}
		item := AnalysisInfoItem{
			Name:            firstString([]map[string]json.RawMessage{fields}, "Name", "AnalysisName", "Title"),
			Type:            firstString([]map[string]json.RawMessage{fields}, "Type"),
			Fields:          firstString([]map[string]json.RawMessage{fields}, "Fields"),
			QueryIndex:      firstInt([]map[string]json.RawMessage{fields}, "QueryIndex", "Index"),
			Limit:           firstInt([]map[string]json.RawMessage{fields}, "Limit"),
			Configuration:   cloneRaw(rawByNameOrNil(fields, "Configuration", "Config", "AnalysisConfig", "ConfigInfo")),
			ColNames:        cloneRaw(rawByNameOrNil(fields, "ColNames")),
			Columns:         cloneRaw(rawByNameOrNil(fields, "Columns")),
			RawResult:       cloneRaw(rawByNameOrNil(fields, "RawResult", "RawResults", "AnalysisOriginal", "Result")),
			FormattedResult: cloneRaw(rawByNameOrNil(fields, "FormattedResult", "AnalysisResultFormat")),
			Error:           parseAnalysisError(fields),
		}
		configFields, queryIndex, limit := parseAnalysisConfig(rawByNameOrNil(fields, "ConfigInfo"))
		if item.Fields == "" {
			item.Fields = configFields
		}
		if item.QueryIndex == 0 {
			item.QueryIndex = queryIndex
		}
		if item.Limit == 0 {
			item.Limit = limit
		}
		items = append(items, item)
	}
	return items, nil
}

func parseAnalysisConfig(raw json.RawMessage) (fields string, queryIndex, limit int64) {
	if len(raw) == 0 {
		return "", 0, 0
	}
	var entries []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return "", 0, 0
	}
	for _, entry := range entries {
		switch strings.ToLower(strings.TrimSpace(entry.Key)) {
		case "fields":
			fields = strings.TrimSpace(entry.Value)
		case "queryindex", "index":
			queryIndex, _ = strconv.ParseInt(strings.TrimSpace(entry.Value), 10, 64)
		case "limit":
			limit, _ = strconv.ParseInt(strings.TrimSpace(entry.Value), 10, 64)
		}
	}
	return fields, queryIndex, limit
}

func parseAnalysisError(fields map[string]json.RawMessage) *AnalysisItemError {
	raw := rawByNameOrNil(fields, "Error", "ErrorInfo")
	if len(raw) == 0 {
		message := firstString([]map[string]json.RawMessage{fields}, "ErrorMessage", "ErrorMsg")
		if message == "" {
			return nil
		}
		return &AnalysisItemError{Message: message}
	}
	var value struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if json.Unmarshal(raw, &value) == nil {
		if value.Message == "" {
			var message string
			if json.Unmarshal(raw, &message) == nil {
				value.Message = message
			}
		}
		if value.Code != "" || value.Message != "" {
			return &AnalysisItemError{Code: value.Code, Message: value.Message}
		}
	}
	return &AnalysisItemError{Message: "provider analysis error"}
}

var operationalFieldNames = map[string]struct{}{
	"uin": {}, "alarm": {}, "topic": {}, "topicid": {}, "topicname": {}, "logset": {}, "logsetid": {}, "logsetname": {}, "region": {},
	"alertid": {}, "alarmid": {}, "recordid": {}, "firetime": {}, "alerttime": {}, "alarmtime": {}, "eventtime": {}, "timestamp": {},
	"starttime": {}, "endtime": {}, "query": {}, "queryparams": {}, "queryinterval": {}, "resultcount": {}, "count": {}, "condition": {}, "triggerparams": {},
}

func selectOperationalFields(scopes []map[string]json.RawMessage) map[string]json.RawMessage {
	fields := make(map[string]json.RawMessage)
	for _, scope := range scopes {
		for name, value := range scope {
			if _, ok := operationalFieldNames[strings.ToLower(name)]; ok {
				if _, exists := fields[name]; !exists {
					fields[name] = cloneRaw(value)
				}
			}
		}
	}
	return fields
}

func findObjectByKey(value map[string]json.RawMessage, key string, depth int) (map[string]json.RawMessage, map[string]json.RawMessage, bool) {
	if depth > 6 {
		return nil, nil, false
	}
	for name, raw := range value {
		if strings.EqualFold(name, key) {
			object, err := decodeObject(raw)
			if err == nil {
				return object, value, true
			}
		}
	}
	for _, raw := range value {
		var child map[string]json.RawMessage
		if json.Unmarshal(raw, &child) != nil || child == nil {
			continue
		}
		if object, parent, ok := findObjectByKey(child, key, depth+1); ok {
			return object, parent, true
		}
	}
	return nil, nil, false
}

func rawByName(fields map[string]json.RawMessage, names ...string) (json.RawMessage, bool) {
	for _, name := range names {
		for actual, raw := range fields {
			if strings.EqualFold(actual, name) {
				return raw, true
			}
		}
	}
	return nil, false
}

func rawByNameOrNil(fields map[string]json.RawMessage, names ...string) json.RawMessage {
	raw, _ := rawByName(fields, names...)
	return raw
}

func firstString(scopes []map[string]json.RawMessage, names ...string) string {
	for _, scope := range scopes {
		if raw, ok := rawByName(scope, names...); ok {
			var value string
			if json.Unmarshal(raw, &value) == nil {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func firstInt(scopes []map[string]json.RawMessage, names ...string) int64 {
	for _, scope := range scopes {
		if raw, ok := rawByName(scope, names...); ok {
			var value int64
			if json.Unmarshal(raw, &value) == nil {
				return value
			}
		}
	}
	return 0
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}
