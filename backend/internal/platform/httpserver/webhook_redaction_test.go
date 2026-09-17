package httpserver

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccessLogNeverDumpsWebhookCapabilitiesEvenInDebugMode(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hooks/{token}", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.ReadAll(request.Body)
		writer.WriteHeader(http.StatusForbidden)
	})
	secret := "signed-subscription-token"
	pathToken := "opaque-webhook-token"
	request := httptest.NewRequest(http.MethodPost, "/hooks/"+pathToken, strings.NewReader(`{"Token":"`+secret+`","SubscribeURL":"https://sns.us-east-1.amazonaws.com/?Token=`+secret+`"}`))
	request.Header.Set("x-amz-sns-message-type", "SubscriptionConfirmation")
	AccessLog(logger, 1<<20, true, mux).ServeHTTP(httptest.NewRecorder(), request)

	logged := output.String()
	for _, forbidden := range []string{secret, pathToken, "SubscribeURL", "x-amz-sns-message-type", "request_body", "http.path"} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("webhook access log contains %q: %s", forbidden, logged)
		}
	}
	if !strings.Contains(logged, `"route":"POST /hooks/{token}"`) {
		t.Fatalf("webhook route pattern missing: %s", logged)
	}
}

func TestAccessLogRedactsWebhookCapabilitiesForMethodMismatch(t *testing.T) {
	for _, requestDebug := range []bool{false, true} {
		t.Run(fmt.Sprintf("debug_%t", requestDebug), func(t *testing.T) {
			var output bytes.Buffer
			logger := testLogger(t, &output)
			mux := http.NewServeMux()
			mux.HandleFunc("POST /hooks/{token}", func(http.ResponseWriter, *http.Request) {})
			secret := "opaque-method-mismatch-token"
			request := httptest.NewRequest(http.MethodGet, "/hooks/"+secret, strings.NewReader(`{"SubscribeURL":"https://sns.us-east-1.amazonaws.com/?Token=`+secret+`"}`))
			request.Header.Set("x-amz-sns-message-type", "SubscriptionConfirmation")
			AccessLog(logger, 1<<20, requestDebug, mux).ServeHTTP(httptest.NewRecorder(), request)

			logged := output.String()
			for _, forbidden := range []string{secret, "SubscribeURL", "x-amz-sns-message-type", "request_body", "http.path"} {
				if strings.Contains(logged, forbidden) {
					t.Fatalf("method-mismatched webhook access log contains %q: %s", forbidden, logged)
				}
			}
		})
	}
}
