package platform

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mendry/backend/internal/modules/notifications/domain"
)

var tokenPattern = regexp.MustCompile(`^[0-9]{1,20}:[A-Za-z0-9_-]{10,200}$`)
var chatPattern = regexp.MustCompile(`^(-?[0-9]{1,24}|@[A-Za-z0-9_]{5,64})$`)
var feishuPath = regexp.MustCompile(`^/open-apis/bot/v2/hook/[A-Za-z0-9-]{10,100}$`)
var slackPath = regexp.MustCompile(`^/services/T[A-Z0-9]{6,20}/B[A-Z0-9]{6,20}/[A-Za-z0-9]{20,40}$`)
var discordPath = regexp.MustCompile(`^/api/webhooks/[0-9]{15,25}/[A-Za-z0-9_-]{30,200}$`)
var whatsappTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,1000}$`)
var whatsappPhoneIDPattern = regexp.MustCompile(`^[0-9]{5,30}$`)
var whatsappRecipientPattern = regexp.MustCompile(`^\+?[1-9][0-9]{6,14}$`)

// whatsappAPIVersion pins the Meta Graph API version used by the WhatsApp Cloud API.
const whatsappAPIVersion = "v23.0"

func Validate(platform string, c domain.Credentials) error {
	if len(c.SigningSecret) > 500 {
		return domain.ErrInvalidInput
	}
	if platform == "telegram" {
		if !tokenPattern.MatchString(c.BotToken) || !chatPattern.MatchString(c.ChatID) || c.WebhookURL != "" || c.SigningSecret != "" || c.PhoneNumberID != "" {
			return domain.ErrInvalidInput
		}
		return nil
	}
	if platform == "whatsapp" {
		if !whatsappTokenPattern.MatchString(c.BotToken) || !whatsappPhoneIDPattern.MatchString(c.PhoneNumberID) || !whatsappRecipientPattern.MatchString(c.ChatID) || c.WebhookURL != "" || c.SigningSecret != "" {
			return domain.ErrInvalidInput
		}
		return nil
	}
	if c.BotToken != "" || c.ChatID != "" || c.PhoneNumberID != "" {
		return domain.ErrInvalidInput
	}
	u, err := url.Parse(c.WebhookURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" || u.RawPath != "" {
		return domain.ErrInvalidInput
	}
	switch platform {
	case "feishu":
		if u.Host != "open.feishu.cn" || !feishuPath.MatchString(u.Path) || u.RawQuery != "" {
			return domain.ErrInvalidInput
		}
	case "wecom":
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil || u.Host != "qyapi.weixin.qq.com" || u.Path != "/cgi-bin/webhook/send" || len(q) != 1 || len(q["key"]) != 1 || len(q.Get("key")) < 10 || len(q.Get("key")) > 200 || c.SigningSecret != "" {
			return domain.ErrInvalidInput
		}
	case "slack":
		if u.Host != "hooks.slack.com" || !slackPath.MatchString(u.Path) || u.RawQuery != "" || c.SigningSecret != "" {
			return domain.ErrInvalidInput
		}
	case "discord":
		if u.Host != "discord.com" || !discordPath.MatchString(u.Path) || u.RawQuery != "" || c.SigningSecret != "" {
			return domain.ErrInvalidInput
		}
	default:
		return domain.ErrInvalidInput
	}
	return nil
}

type Sender struct {
	client *http.Client
	now    func() time.Time
}

func NewSender() *Sender {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Sender{client: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("notification redirects are forbidden") }}, now: time.Now}
}
func (s *Sender) Send(ctx context.Context, platform string, c domain.Credentials, message string) error {
	if err := Validate(platform, c); err != nil {
		return err
	}
	endpoint := c.WebhookURL
	payload := map[string]any{}
	authorization := ""
	switch platform {
	case "telegram":
		endpoint = "https://api.telegram.org/bot" + c.BotToken + "/sendMessage"
		payload = map[string]any{"chat_id": c.ChatID, "text": message, "disable_web_page_preview": true}
	case "feishu":
		payload = map[string]any{"msg_type": "text", "content": map[string]string{"text": message}}
		if c.SigningSecret != "" {
			timestamp := strconv.FormatInt(s.now().Unix(), 10)
			mac := hmac.New(sha256.New, []byte(timestamp+"\n"+c.SigningSecret))
			payload["timestamp"] = timestamp
			payload["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
		}
	case "wecom":
		payload = map[string]any{"msgtype": "text", "text": map[string]string{"content": message}}
	case "slack":
		payload = map[string]any{"text": message}
	case "discord":
		payload = map[string]any{"content": message, "allowed_mentions": map[string]any{"parse": []string{}}}
	case "whatsapp":
		endpoint = "https://graph.facebook.com/" + whatsappAPIVersion + "/" + c.PhoneNumberID + "/messages"
		authorization = "Bearer " + c.BotToken
		payload = map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": strings.TrimPrefix(c.ChatID, "+"), "type": "text", "text": map[string]any{"body": message, "preview_url": false}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("notification encoding failed")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("notification request failed")
	}
	request.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response, err := s.client.Do(request)
	// Network errors include credential-bearing URLs; never return or log the original error.
	if err != nil {
		return errors.New("notification network error")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("notification HTTP status %d", response.StatusCode)
	}
	switch platform {
	case "slack":
		// Slack incoming webhooks acknowledge with the plain-text body "ok".
		text, err := io.ReadAll(io.LimitReader(response.Body, 1024))
		if err != nil || strings.TrimSpace(string(text)) != "ok" {
			return errors.New("notification platform rejected message")
		}
		return nil
	case "discord":
		// Discord webhooks without ?wait=true acknowledge with 204 No Content.
		return nil
	}
	var result struct {
		OK         *bool `json:"ok"`
		Code       *int  `json:"code"`
		StatusCode *int  `json:"StatusCode"`
		ErrCode    *int  `json:"errcode"`
		Messages   []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 32*1024)).Decode(&result); err != nil {
		return errors.New("invalid notification response")
	}
	success := false
	switch platform {
	case "telegram":
		success = result.OK != nil && *result.OK
	case "feishu":
		success = result.Code != nil && *result.Code == 0 || result.StatusCode != nil && *result.StatusCode == 0
	case "wecom":
		success = result.ErrCode != nil && *result.ErrCode == 0
	case "whatsapp":
		success = len(result.Messages) > 0 && result.Messages[0].ID != ""
	}
	if !success {
		return errors.New("notification platform rejected message")
	}
	return nil
}

// EndpointHost documents the only external hosts authorized by this adapter.
func EndpointHost(platform string) string {
	switch strings.ToLower(platform) {
	case "telegram":
		return "api.telegram.org"
	case "feishu":
		return "open.feishu.cn"
	case "wecom":
		return "qyapi.weixin.qq.com"
	case "slack":
		return "hooks.slack.com"
	case "discord":
		return "discord.com"
	case "whatsapp":
		return "graph.facebook.com"
	}
	return ""
}
