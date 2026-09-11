package awssns

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- SNS signature version 1 requires SHA-1 by protocol.
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"mendry/backend/internal/modules/hooks/application"
)

const (
	maxCertificateBytes = 64 * 1024
	maxCacheEntries     = 64
	cacheTTL            = time.Hour
	requestTimeout      = 5 * time.Second
)

var certificatePath = regexp.MustCompile(`^/SimpleNotificationService-[A-Za-z0-9_-]+\.pem$`)

// 商业分区 region 必须经过显式评审；不能从请求值任意拼接受信 hostname。
var commercialRegions = map[string]struct{}{
	"af-south-1": {}, "ap-east-1": {}, "ap-east-2": {}, "ap-northeast-1": {}, "ap-northeast-2": {}, "ap-northeast-3": {},
	"ap-south-1": {}, "ap-south-2": {}, "ap-southeast-1": {}, "ap-southeast-2": {}, "ap-southeast-3": {}, "ap-southeast-4": {},
	"ap-southeast-5": {}, "ap-southeast-6": {}, "ap-southeast-7": {}, "ca-central-1": {}, "ca-west-1": {}, "eu-central-1": {}, "eu-central-2": {},
	"eu-north-1": {}, "eu-south-1": {}, "eu-south-2": {}, "eu-west-1": {}, "eu-west-2": {}, "eu-west-3": {},
	"il-central-1": {}, "me-central-1": {}, "me-south-1": {}, "mx-central-1": {}, "sa-east-1": {},
	"us-east-1": {}, "us-east-2": {}, "us-west-1": {}, "us-west-2": {},
}

type envelope struct {
	Type             string `json:"Type"`
	MessageID        string `json:"MessageId"`
	TopicARN         string `json:"TopicArn"`
	Subject          string `json:"Subject"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	SubscribeURL     string `json:"SubscribeURL"`
	Token            string `json:"Token"`
}

type topicIdentity struct {
	region, account string
}

type cachedCertificate struct {
	certificate *x509.Certificate
	expiresAt   time.Time
}

// Options 注入 bounded HTTP client 和时钟；默认 client 禁止 redirect。
type Options struct {
	HTTPClient *http.Client
	Now        func() time.Time
}

// Verifier 实现 SNS 签名验证、证书 cache 和订阅确认。
type Verifier struct {
	client *http.Client
	now    func() time.Time
	mu     sync.Mutex
	cache  map[string]cachedCertificate
}

// NewVerifier 构造不跟随 redirect 的 SNS verifier。
func NewVerifier(options Options) (*Verifier, error) {
	base := options.HTTPClient
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	client.Timeout = 0
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Verifier{client: &client, now: now, cache: make(map[string]cachedCertificate)}, nil
}

// Verify 验证 envelope 和 CloudWatch alarm；control confirmation 在返回前完成。
func (v *Verifier) Verify(ctx context.Context, raw, allowedTopicARN string) (application.VerifiedAWSMessage, error) {
	allowed, err := parseTopicARN(allowedTopicARN)
	if err != nil {
		return application.VerifiedAWSMessage{}, fmt.Errorf("%w: configured topic", application.ErrInvalidAWSSNS)
	}
	var message envelope
	if err := decodeWithoutDuplicateKeys([]byte(raw), &message); err != nil {
		return application.VerifiedAWSMessage{}, fmt.Errorf("%w: envelope", application.ErrInvalidAWSSNS)
	}
	if message.TopicARN != allowedTopicARN {
		return application.VerifiedAWSMessage{}, application.ErrForbiddenAWSSNS
	}
	if err := validateEnvelope(message); err != nil {
		return application.VerifiedAWSMessage{}, err
	}
	certURL, err := validateProviderURL(message.SigningCertURL, allowed.region, certificatePath)
	if err != nil {
		return application.VerifiedAWSMessage{}, application.ErrForbiddenAWSSNS
	}
	certificate, err := v.certificate(ctx, certURL)
	if err != nil {
		return application.VerifiedAWSMessage{}, err
	}
	if err := verifySignature(message, certificate); err != nil {
		return application.VerifiedAWSMessage{}, application.ErrForbiddenAWSSNS
	}
	snsTime, err := time.Parse(time.RFC3339Nano, message.Timestamp)
	if err != nil {
		return application.VerifiedAWSMessage{}, fmt.Errorf("%w: timestamp", application.ErrInvalidAWSSNS)
	}
	verified := application.VerifiedAWSMessage{
		Type: application.AWSMessageType(message.Type), MessageID: message.MessageID,
		TopicARN: message.TopicARN, SNSTimestamp: snsTime.UTC(),
	}
	switch verified.Type {
	case application.AWSMessageSubscriptionConfirmation:
		confirmationURL, err := validateConfirmationURL(message, allowed.region)
		if err != nil {
			return application.VerifiedAWSMessage{}, application.ErrForbiddenAWSSNS
		}
		if err := v.confirm(ctx, confirmationURL); err != nil {
			return application.VerifiedAWSMessage{}, err
		}
		return verified, nil
	case application.AWSMessageUnsubscribeConfirmation:
		// UnsubscribeConfirmation is not called, but its signed capability must
		// still satisfy the same control URL contract.
		if _, err := validateConfirmationURL(message, allowed.region); err != nil {
			return application.VerifiedAWSMessage{}, application.ErrForbiddenAWSSNS
		}
		return verified, nil
	case application.AWSMessageNotification:
		alarm, err := parseAlarm(message.Message, allowed)
		if err != nil {
			return application.VerifiedAWSMessage{}, err
		}
		verified.Alarm = &alarm
		return verified, nil
	default:
		return application.VerifiedAWSMessage{}, fmt.Errorf("%w: message type", application.ErrInvalidAWSSNS)
	}
}

func validateEnvelope(message envelope) error {
	if strings.TrimSpace(message.MessageID) == "" || strings.TrimSpace(message.Message) == "" || strings.TrimSpace(message.TopicARN) == "" ||
		strings.TrimSpace(message.Timestamp) == "" || strings.TrimSpace(message.Signature) == "" || strings.TrimSpace(message.SigningCertURL) == "" {
		return fmt.Errorf("%w: required envelope field", application.ErrInvalidAWSSNS)
	}
	if len(message.MessageID) > 128 || len(message.TopicARN) > 512 || len(message.Subject) > 256 || len(message.Timestamp) > 64 ||
		len(message.Signature) > 4096 || len(message.SigningCertURL) > 2048 || len(message.SubscribeURL) > 8192 || len(message.Token) > 4096 {
		return fmt.Errorf("%w: envelope bounds", application.ErrInvalidAWSSNS)
	}
	switch message.Type {
	case string(application.AWSMessageNotification):
	case string(application.AWSMessageSubscriptionConfirmation), string(application.AWSMessageUnsubscribeConfirmation):
		if strings.TrimSpace(message.Token) == "" || strings.TrimSpace(message.SubscribeURL) == "" {
			return fmt.Errorf("%w: control field", application.ErrInvalidAWSSNS)
		}
	default:
		return fmt.Errorf("%w: message type", application.ErrInvalidAWSSNS)
	}
	if message.SignatureVersion != "1" && message.SignatureVersion != "2" {
		return fmt.Errorf("%w: signature version", application.ErrInvalidAWSSNS)
	}
	return nil
}

func parseTopicARN(value string) (topicIdentity, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 6 || parts[0] != "arn" || parts[1] != "aws" || parts[2] != "sns" || len(parts[4]) != 12 ||
		len(parts[5]) == 0 || len(parts[5]) > 256 || strings.HasSuffix(parts[5], ".fifo") || !validTopicName(parts[5]) {
		return topicIdentity{}, errors.New("invalid topic ARN")
	}
	if _, ok := commercialRegions[parts[3]]; !ok {
		return topicIdentity{}, errors.New("unsupported region")
	}
	for _, digit := range parts[4] {
		if digit < '0' || digit > '9' {
			return topicIdentity{}, errors.New("invalid account")
		}
	}
	return topicIdentity{region: parts[3], account: parts[4]}, nil
}

func validTopicName(value string) bool {
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return value != ""
}

func validateProviderURL(raw, region string, pathPattern *regexp.Regexp) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() != "sns."+region+".amazonaws.com" ||
		(parsed.Port() != "" && parsed.Port() != "443") || parsed.RawQuery != "" || parsed.Fragment != "" || !pathPattern.MatchString(parsed.EscapedPath()) {
		return nil, errors.New("untrusted SNS URL")
	}
	return parsed, nil
}

func validateConfirmationURL(message envelope, region string) (*url.URL, error) {
	parsed, err := url.Parse(message.SubscribeURL)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() != "sns."+region+".amazonaws.com" ||
		(parsed.Port() != "" && parsed.Port() != "443") || parsed.EscapedPath() != "/" || parsed.Fragment != "" {
		return nil, errors.New("untrusted confirmation URL")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(query) != 3 || len(query["Action"]) != 1 || len(query["TopicArn"]) != 1 || len(query["Token"]) != 1 ||
		query.Get("Action") != "ConfirmSubscription" || query.Get("TopicArn") != message.TopicARN || query.Get("Token") != message.Token {
		return nil, errors.New("confirmation URL mismatch")
	}
	return parsed, nil
}

func (v *Verifier) certificate(ctx context.Context, endpoint *url.URL) (*x509.Certificate, error) {
	key := endpoint.String()
	now := v.now().UTC()
	v.mu.Lock()
	if entry, ok := v.cache[key]; ok && now.Before(entry.expiresAt) {
		v.mu.Unlock()
		return entry.certificate, nil
	}
	v.mu.Unlock()

	body, err := v.get(ctx, endpoint, maxCertificateBytes)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, application.ErrForbiddenAWSSNS
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || certificate.PublicKeyAlgorithm != x509.RSA || now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
		return nil, application.ErrForbiddenAWSSNS
	}
	expires := now.Add(cacheTTL)
	if certificate.NotAfter.Before(expires) {
		expires = certificate.NotAfter
	}
	v.mu.Lock()
	if len(v.cache) >= maxCacheEntries {
		for existing := range v.cache {
			delete(v.cache, existing)
			break
		}
	}
	v.cache[key] = cachedCertificate{certificate: certificate, expiresAt: expires}
	v.mu.Unlock()
	return certificate, nil
}

func (v *Verifier) confirm(ctx context.Context, endpoint *url.URL) error {
	_, err := v.get(ctx, endpoint, 64*1024)
	return err
}

func (v *Verifier) get(ctx context.Context, endpoint *url.URL, limit int64) ([]byte, error) {
	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, application.ErrForbiddenAWSSNS
	}
	response, err := v.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: SNS request", application.ErrTemporaryAWSSNS)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, application.ErrForbiddenAWSSNS
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: SNS status", application.ErrTemporaryAWSSNS)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: SNS response", application.ErrTemporaryAWSSNS)
	}
	if int64(len(body)) > limit {
		return nil, application.ErrForbiddenAWSSNS
	}
	return body, nil
}

func verifySignature(message envelope, certificate *x509.Certificate) error {
	publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok || publicKey.N.BitLen() < 2048 {
		return errors.New("invalid RSA key")
	}
	canonical, err := canonicalMessage(message)
	if err != nil {
		return err
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(message.Signature)
	if err != nil {
		return err
	}
	if message.SignatureVersion == "1" {
		digest := sha1.Sum(canonical) // #nosec G401 -- required by SNS v1.
		return rsa.VerifyPKCS1v15(publicKey, crypto.SHA1, digest[:], signature)
	}
	digest := sha256.Sum256(canonical)
	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature)
}

func canonicalMessage(message envelope) ([]byte, error) {
	var fields [][2]string
	switch message.Type {
	case string(application.AWSMessageNotification):
		fields = append(fields, [2]string{"Message", message.Message}, [2]string{"MessageId", message.MessageID})
		if message.Subject != "" {
			fields = append(fields, [2]string{"Subject", message.Subject})
		}
		fields = append(fields, [2]string{"Timestamp", message.Timestamp}, [2]string{"TopicArn", message.TopicARN}, [2]string{"Type", message.Type})
	case string(application.AWSMessageSubscriptionConfirmation), string(application.AWSMessageUnsubscribeConfirmation):
		fields = [][2]string{{"Message", message.Message}, {"MessageId", message.MessageID}, {"SubscribeURL", message.SubscribeURL},
			{"Timestamp", message.Timestamp}, {"Token", message.Token}, {"TopicArn", message.TopicARN}, {"Type", message.Type}}
	default:
		return nil, errors.New("unsupported message type")
	}
	var builder strings.Builder
	for _, field := range fields {
		builder.WriteString(field[0])
		builder.WriteByte('\n')
		builder.WriteString(field[1])
		builder.WriteByte('\n')
	}
	return []byte(builder.String()), nil
}

type cloudWatchEnvelope struct {
	AlarmName        string          `json:"AlarmName"`
	AlarmDescription string          `json:"AlarmDescription"`
	AlarmARN         string          `json:"AlarmArn"`
	NewStateValue    string          `json:"NewStateValue"`
	NewStateReason   string          `json:"NewStateReason"`
	StateChangeTime  string          `json:"StateChangeTime"`
	Region           string          `json:"Region"`
	AlarmRule        string          `json:"AlarmRule"`
	Trigger          json.RawMessage `json:"Trigger"`
}

func parseAlarm(raw string, topic topicIdentity) (application.AWSCloudWatchAlarm, error) {
	var value cloudWatchEnvelope
	if err := decodeWithoutDuplicateKeys([]byte(raw), &value); err != nil {
		return application.AWSCloudWatchAlarm{}, fmt.Errorf("%w: alarm payload", application.ErrInvalidAWSSNS)
	}
	arn := strings.SplitN(value.AlarmARN, ":", 7)
	if len(arn) != 7 || arn[0] != "arn" || arn[1] != "aws" || arn[2] != "cloudwatch" || arn[3] != topic.region ||
		arn[4] != topic.account || arn[5] != "alarm" || strings.TrimSpace(arn[6]) == "" || strings.TrimSpace(value.AlarmName) == "" {
		return application.AWSCloudWatchAlarm{}, fmt.Errorf("%w: alarm identity", application.ErrInvalidAWSSNS)
	}
	if value.NewStateValue != "ALARM" && value.NewStateValue != "OK" && value.NewStateValue != "INSUFFICIENT_DATA" {
		return application.AWSCloudWatchAlarm{}, fmt.Errorf("%w: alarm state", application.ErrInvalidAWSSNS)
	}
	changedAt, err := parseStateChangeTime(value.StateChangeTime)
	if err != nil {
		return application.AWSCloudWatchAlarm{}, fmt.Errorf("%w: state time", application.ErrInvalidAWSSNS)
	}
	if len(value.AlarmName) > 255 || len(value.AlarmARN) > 2048 || len(value.AlarmDescription) > 2048 || len(value.NewStateReason) > 4096 || len(value.AlarmRule) > 8192 || len(value.Trigger) > 32768 {
		return application.AWSCloudWatchAlarm{}, fmt.Errorf("%w: alarm bounds", application.ErrInvalidAWSSNS)
	}
	trigger := json.RawMessage(nil)
	if len(value.Trigger) > 0 && string(value.Trigger) != "null" {
		var object map[string]any
		if err := decodeWithoutDuplicateKeys(value.Trigger, &object); err != nil {
			return application.AWSCloudWatchAlarm{}, fmt.Errorf("%w: alarm trigger", application.ErrInvalidAWSSNS)
		}
		trigger = append(json.RawMessage(nil), value.Trigger...)
	}
	return application.AWSCloudWatchAlarm{
		Name: strings.TrimSpace(value.AlarmName), ARN: value.AlarmARN, State: value.NewStateValue,
		Reason: value.NewStateReason, StateChangedAt: changedAt.UTC(), Region: topic.region, AccountID: topic.account,
		Description: value.AlarmDescription, AlarmRule: value.AlarmRule, Trigger: trigger,
	}, nil
}

func parseStateChangeTime(value string) (time.Time, error) {
	// CloudWatch emits both RFC3339 offsets and the documented basic +0000
	// offset. Keep accepted layouts explicit so malformed timestamps fail closed.
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999-0700"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("unsupported CloudWatch state timestamp")
}

func decodeWithoutDuplicateKeys(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := consumeUniqueValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) || token != nil {
		return errors.New("JSON contains multiple values")
	}
	return json.Unmarshal(raw, target)
}

func consumeUniqueValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is invalid")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate JSON key")
			}
			seen[key] = struct{}{}
			if err := consumeUniqueValue(decoder); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := consumeUniqueValue(decoder); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
}
