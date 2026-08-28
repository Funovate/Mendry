package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

const (
	maxTencentCallbackBytes = 64 << 10
	maxTencentTopicID       = 255
	maxTencentDetailURL     = 2048
	tencentCLSFingerprintV1 = "tencent-cls:v1"
)

const tencentCLSRegionalMonitorSuffix = "-monitor.cls.tencentcs.com"

// ErrInvalidTencentCallback 表示入站 Tencent CLS callback 不符合同步校验契约。
var ErrInvalidTencentCallback = errors.New("invalid tencent cls callback")

// IsValidTencentDetailURL 校验受信任 Tencent CLS detail URL 的 HTTPS、host 和 path 约束。
func IsValidTencentDetailURL(value string) bool {
	return validTencentDetailURL(value)
}

// TencentCLSCallback 是已校验的 aggregate callback envelope。RawFields 保留 provider 字段供 trusted evidence adapter 使用，但不会作为 model authority 或 URL-fetch capability。
type TencentCLSCallback struct {
	TopicID   string
	DetailURL string
	RawFields map[string]json.RawMessage
}

// EvidenceFields 返回不含 DetailUrl capability 的 provider 字段投影；TopicId
// 保留为证据身份，但不会进入语义 fingerprint 或模型的任意 URL 权限。
func (c TencentCLSCallback) EvidenceFields() map[string]json.RawMessage {
	fields := make(map[string]json.RawMessage, 6)
	for _, key := range []string{"UIN", "Alarm", "Topic", "TopicId", "Condition", "TriggerParams"} {
		if value, ok := c.RawFields[key]; ok {
			fields[key] = append(json.RawMessage(nil), value...)
		}
	}
	return fields
}

// ParseTencentCLSCallback 只同步校验 callback contract，故意不在此处请求 provider detail page。
func ParseTencentCLSCallback(raw, contentType string) (TencentCLSCallback, error) {
	if normalizeMediaType(contentType) != "application/json" || len(raw) == 0 || len(raw) > maxTencentCallbackBytes {
		return TencentCLSCallback{}, ErrInvalidTencentCallback
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.UseNumber()
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return TencentCLSCallback{}, ErrInvalidTencentCallback
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return TencentCLSCallback{}, ErrInvalidTencentCallback
	}
	topicID, ok := requiredStringField(fields, "TopicId", maxTencentTopicID)
	if !ok {
		return TencentCLSCallback{}, ErrInvalidTencentCallback
	}
	detailURL, ok := requiredStringField(fields, "DetailUrl", maxTencentDetailURL)
	if !ok || !validTencentDetailURL(detailURL) {
		return TencentCLSCallback{}, ErrInvalidTencentCallback
	}
	for _, field := range []string{"UIN", "Alarm", "Topic", "Condition", "TriggerParams"} {
		if rawField, exists := fields[field]; exists {
			var value string
			if err := json.Unmarshal(rawField, &value); err != nil || len(value) > 4096 {
				return TencentCLSCallback{}, fmt.Errorf("%w: %s is invalid", ErrInvalidTencentCallback, field)
			}
		}
	}
	return TencentCLSCallback{TopicID: topicID, DetailURL: detailURL, RawFields: fields}, nil
}

// SemanticPayload 从 semantic fingerprint 中排除 provider identity、query-count metadata 和 URL capability，同时单独保留 raw callback 用于 evidence persistence。
func (c TencentCLSCallback) SemanticPayload() []byte {
	fields := make(map[string]json.RawMessage, len(c.RawFields))
	for key, value := range c.RawFields {
		switch strings.ToLower(key) {
		case "topicid", "detailurl", "uin", "condition", "triggerparams":
			continue
		default:
			fields[key] = value
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return []byte(`{}`)
	}
	return encoded
}

// semanticFingerprint 固定使用 callback 的 Alarm 和 Topic；LLM 只生成标题，
// 不得通过增删或改写 grouping_fields 改变 Tencent CLS 事故身份。
func (c TencentCLSCallback) semanticFingerprint(projectID, sourceID string) (string, error) {
	fields := map[string]string{
		"alarm": tencentSemanticField(c.RawFields, "Alarm"),
		"topic": tencentSemanticField(c.RawFields, "Topic"),
	}
	return hashGrouping(tencentCLSFingerprintV1, projectID, sourceID, fields)
}

func tencentSemanticField(fields map[string]json.RawMessage, key string) string {
	value, _ := requiredStringField(fields, key, maxTencentCallbackBytes)
	return collapseSpace(value)
}

func requiredStringField(fields map[string]json.RawMessage, key string, maximum int) (string, bool) {
	raw, ok := fields[key]
	if !ok {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, len(value) > 0 && len(value) <= maximum
}

func validTencentDetailURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" || len(value) > maxTencentDetailURL {
		return false
	}
	hostname := strings.ToLower(parsed.Hostname())
	switch hostname {
	case "console.cloud.tencent.com", "alarm.cls.tencentcs.com", "alarm.tencentcloud.com", "alarm.tencentcloudapi.com", "cls.tencentcloud.com":
		return parsed.Path != ""
	default:
		return isTencentCLSRegionalMonitorHost(hostname) && parsed.Path != ""
	}
}

// isTencentCLSRegionalMonitorHost 接受 CLS 短链实际使用的区域详情主机，
// 同时限制区域 label 形状，避免把重定向 allowlist 放宽为任意 Tencent 子域名。
func isTencentCLSRegionalMonitorHost(hostname string) bool {
	region, ok := strings.CutSuffix(hostname, tencentCLSRegionalMonitorSuffix)
	if !ok || len(region) == 0 || len(region)+len("-monitor") > 63 {
		return false
	}
	parts := strings.Split(region, "-")
	if len(parts) < 2 || len(parts[0]) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	for _, char := range region {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	for _, char := range parts[0] {
		if char < 'a' || char > 'z' {
			return false
		}
	}
	return true
}

func normalizeMediaType(value string) string {
	mediaType, _, _ := strings.Cut(value, ";")
	return strings.ToLower(strings.TrimSpace(mediaType))
}
