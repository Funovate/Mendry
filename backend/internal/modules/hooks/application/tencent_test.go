package application_test

import (
	"errors"
	"strings"
	"testing"

	"mendry/backend/internal/modules/hooks/application"
)

func TestParseTencentCLSCallbackAcceptsBoundedJSONEnvelope(t *testing.T) {
	callback, err := application.ParseTencentCLSCallback(`{
  "UIN": "100013370924",
  "Alarm": "测试信息",
  "Topic": "payment",
  "TopicId": "topic-123",
  "Condition": "[$1.__QUERYCOUNT__] > 0",
  "TriggerParams": "$1.__QUERYCOUNT__=1;",
	  "DetailUrl": "https://alarm.cls.tencentcs.com/4wOSDecA"
}`, "application/json; charset=utf-8")
	if err != nil {
		t.Fatalf("ParseTencentCLSCallback() error = %v", err)
	}
	if callback.TopicID != "topic-123" || callback.DetailURL == "" || len(callback.RawFields) != 7 {
		t.Fatalf("callback = %#v", callback)
	}
	semantic := string(callback.SemanticPayload())
	for _, excluded := range []string{"TopicId", "DetailUrl", "UIN", "Condition", "TriggerParams"} {
		if strings.Contains(semantic, excluded) {
			t.Fatalf("semantic payload retained %s: %s", excluded, semantic)
		}
	}
	if !strings.Contains(semantic, "Alarm") || !strings.Contains(semantic, "Topic") {
		t.Fatalf("semantic payload dropped grouping fields: %s", semantic)
	}
}

func TestIsValidTencentDetailURLEnforcesRegionalMonitorShape(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{
			name: "regional monitor host",
			url:  "https://eu-frankfurt-monitor.cls.tencentcs.com/cls_no_login?action=GetAlertDetail",
			want: true,
		},
		{
			name: "invalid region prefix",
			url:  "https://europe-frankfurt-monitor.cls.tencentcs.com/cls_no_login",
			want: false,
		},
		{
			name: "empty region label",
			url:  "https://eu--frankfurt-monitor.cls.tencentcs.com/cls_no_login",
			want: false,
		},
		{
			name: "lookalike suffix",
			url:  "https://eu-frankfurt-monitor.cls.tencentcs.com.evil.example/cls_no_login",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := application.IsValidTencentDetailURL(tc.url); got != tc.want {
				t.Fatalf("IsValidTencentDetailURL(%q) = %t, want %t", tc.url, got, tc.want)
			}
		})
	}
}

func TestParseTencentCLSCallbackRejectsInvalidEnvelope(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		raw         string
	}{
		{name: "wrong content type", contentType: "text/plain", raw: `{"TopicId":"topic","DetailUrl":"https://console.cloud.tencent.com/cls/alert"}`},
		{name: "malformed json", contentType: "application/json", raw: `{"TopicId":"topic"`},
		{name: "missing topic id", contentType: "application/json", raw: `{"DetailUrl":"https://console.cloud.tencent.com/cls/alert"}`},
		{name: "missing detail url", contentType: "application/json", raw: `{"TopicId":"topic"}`},
		{name: "wrong detail host", contentType: "application/json", raw: `{"TopicId":"topic","DetailUrl":"https://evil.example/cls/alert"}`},
		{name: "userinfo detail url", contentType: "application/json", raw: `{"TopicId":"topic","DetailUrl":"https://user@console.cloud.tencent.com/cls/alert"}`},
		{name: "trailing json value", contentType: "application/json", raw: `{"TopicId":"topic","DetailUrl":"https://console.cloud.tencent.com/cls/alert"}{}`},
		{name: "non-string provider field", contentType: "application/json", raw: `{"TopicId":"topic","Alarm":1,"DetailUrl":"https://console.cloud.tencent.com/cls/alert"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := application.ParseTencentCLSCallback(tc.raw, tc.contentType)
			if !errors.Is(err, application.ErrInvalidTencentCallback) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
