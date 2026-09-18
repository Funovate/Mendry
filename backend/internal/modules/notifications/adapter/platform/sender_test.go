package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mendry/backend/internal/modules/notifications/domain"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestValidateRejectsUnsafeDestinations(t *testing.T) {
	good := map[string]domain.Credentials{
		"telegram": {BotToken: "123456:abcdefghijklmnop", ChatID: "-100123456789"},
		"feishu":   {WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/abcdefghijklmnop"},
		"wecom":    {WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abcdefghijklmnop"},
	}
	for platform, c := range good {
		if err := Validate(platform, c); err != nil {
			t.Fatalf("valid %s: %v", platform, err)
		}
	}
	for _, endpoint := range []string{
		"http://open.feishu.cn/open-apis/bot/v2/hook/abcdefghijklmnop",
		"https://open.feishu.cn.evil.test/open-apis/bot/v2/hook/abcdefghijklmnop",
		"https://127.0.0.1/open-apis/bot/v2/hook/abcdefghijklmnop",
		"https://user:secret@open.feishu.cn/open-apis/bot/v2/hook/abcdefghijklmnop",
		"https://open.feishu.cn:443/open-apis/bot/v2/hook/abcdefghijklmnop",
		"https://open.feishu.cn/open-apis/bot/v2/hook/abcdefghijklmnop?redirect=evil",
		"https://open.feishu.cn/open-apis/bot/v2/hook/abcdefghijklmnop#fragment",
		"https://open.feishu.cn/open-apis/bot/v2/hook/%61bcdefghijklmnop",
	} {
		if Validate("feishu", domain.Credentials{WebhookURL: endpoint}) == nil {
			t.Errorf("accepted unsafe URL %s", endpoint)
		}
	}
	if Validate("telegram", domain.Credentials{BotToken: "1:abc/../../other", ChatID: "123"}) == nil {
		t.Fatal("token path injection accepted")
	}
	if Validate("wecom", domain.Credentials{WebhookURL: good["wecom"].WebhookURL + "&key=abcdefghijklmnop"}) == nil {
		t.Fatal("duplicate key accepted")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSenderPayloadAndPlatformAcknowledgement(t *testing.T) {
	cases := []struct {
		platform    string
		credentials domain.Credentials
		response    string
		success     bool
	}{
		{"telegram", domain.Credentials{BotToken: "123456:abcdefghijklmnop", ChatID: "-100123"}, `{"ok":true}`, true},
		{"telegram", domain.Credentials{BotToken: "123456:abcdefghijklmnop", ChatID: "-100123"}, `{"ok":false}`, false},
		{"feishu", domain.Credentials{WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/abcdefghijklmnop", SigningSecret: "secret-signing-value"}, `{"code":0}`, true},
		{"feishu", domain.Credentials{WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/abcdefghijklmnop"}, `{"StatusCode":0}`, true},
		{"wecom", domain.Credentials{WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abcdefghijklmnop"}, `{"errcode":0}`, true},
		{"wecom", domain.Credentials{WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abcdefghijklmnop"}, `{}`, false},
	}
	for _, tt := range cases {
		t.Run(tt.platform+tt.response, func(t *testing.T) {
			sender := NewSender()
			sender.now = func() time.Time { return time.Unix(123, 0) }
			sender.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != "POST" || r.URL.Host != EndpointHost(tt.platform) {
					t.Fatalf("unexpected destination %s", r.URL.Host)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if tt.platform == "telegram" && (body["text"] != "hello\nworld" || body["chat_id"] != "-100123") {
					t.Fatalf("wrong payload %#v", body)
				}
				if tt.credentials.SigningSecret != "" && (body["timestamp"] != "123" || body["sign"] == nil) {
					t.Fatal("sign missing")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tt.response))}, nil
			})
			err := sender.Send(context.Background(), tt.platform, tt.credentials, "hello\nworld")
			if (err == nil) != tt.success {
				t.Fatalf("error=%v success=%t", err, tt.success)
			}
		})
	}
}
func TestSenderNeverLeaksCredentialBearingErrors(t *testing.T) {
	sender := NewSender()
	sender.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { return nil, errors.New("secret URL: " + r.URL.String()) })
	err := sender.Send(context.Background(), "telegram", domain.Credentials{BotToken: "123456:abcdefghijklmnop", ChatID: "-100123"}, "test")
	if err == nil || strings.Contains(err.Error(), "abcdefghijklmnop") {
		t.Fatalf("unsafe error %v", err)
	}
	if sender.client.CheckRedirect(nil, nil) == nil {
		t.Fatal("redirects allowed")
	}
}
