package awssns

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- SNS v1 protocol fixture.
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mendry/backend/internal/modules/hooks/application"
)

const (
	testTopic = "arn:aws:sns:us-east-1:123456789012:mendry-alarms"
	testCert  = "https://sns.us-east-1.amazonaws.com/SimpleNotificationService-test.pem"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestVerifierAcceptsSignedCloudWatchNotificationV1AndV2(t *testing.T) {
	privateKey, certificatePEM := testCertificate(t)
	var fetches atomic.Int32
	verifier := testVerifier(t, certificatePEM, &fetches, nil)
	for _, version := range []string{"1", "2"} {
		stateChangeTime := "2025-09-07T12:00:00.123Z"
		if version == "1" {
			stateChangeTime = "2025-09-07T12:00:00.123+0000"
		}
		message := envelope{
			Type: "Notification", MessageID: "message-" + version, TopicARN: testTopic,
			Message:   `{"AlarmName":"checkout:errors","AlarmDescription":"error rate","AlarmArn":"arn:aws:cloudwatch:us-east-1:123456789012:alarm:checkout:errors","NewStateValue":"ALARM","NewStateReason":"threshold crossed","StateChangeTime":"` + stateChangeTime + `","Region":"US East (N. Virginia)","Trigger":{"MetricName":"Errors","Namespace":"AWS/Lambda","Period":60}}`,
			Timestamp: "2025-09-07T12:00:01Z", SignatureVersion: version, SigningCertURL: testCert,
		}
		if version == "2" {
			message.Subject = "alarm subject\nwith newline"
		}
		signEnvelope(t, privateKey, &message)
		raw, _ := json.Marshal(message)
		verified, err := verifier.Verify(context.Background(), string(raw), testTopic)
		if err != nil {
			t.Fatalf("Verify(v%s) error = %v", version, err)
		}
		if verified.Alarm == nil || verified.Alarm.ARN == "" || verified.Alarm.State != "ALARM" || verified.Alarm.Region != "us-east-1" {
			t.Fatalf("Verify(v%s) = %#v", version, verified)
		}
	}
	if fetches.Load() != 1 {
		t.Fatalf("certificate fetches = %d, want cache hit", fetches.Load())
	}
}

func TestVerifierRejectsTopicMismatchDuplicateKeysAndTampering(t *testing.T) {
	privateKey, certificatePEM := testCertificate(t)
	verifier := testVerifier(t, certificatePEM, nil, nil)
	message := envelope{
		Type: "Notification", MessageID: "message", TopicARN: testTopic,
		Message:   `{"AlarmName":"checkout-errors","AlarmArn":"arn:aws:cloudwatch:us-east-1:123456789012:alarm:checkout-errors","NewStateValue":"OK","NewStateReason":"recovered","StateChangeTime":"2025-09-07T12:00:00Z"}`,
		Timestamp: "2025-09-07T12:00:01Z", SignatureVersion: "2", SigningCertURL: testCert,
	}
	signEnvelope(t, privateKey, &message)
	raw, _ := json.Marshal(message)
	if _, err := verifier.Verify(context.Background(), string(raw), "arn:aws:sns:us-east-1:123456789012:other"); !errors.Is(err, application.ErrForbiddenAWSSNS) {
		t.Fatalf("topic mismatch error = %v", err)
	}
	duplicate := strings.Replace(string(raw), `"Type":"Notification"`, `"Type":"Notification","Type":"Notification"`, 1)
	if _, err := verifier.Verify(context.Background(), duplicate, testTopic); !errors.Is(err, application.ErrInvalidAWSSNS) {
		t.Fatalf("duplicate key error = %v", err)
	}
	message.Message += " "
	tampered, _ := json.Marshal(message)
	if _, err := verifier.Verify(context.Background(), string(tampered), testTopic); !errors.Is(err, application.ErrForbiddenAWSSNS) {
		t.Fatalf("tamper error = %v", err)
	}
}

func TestVerifierConfirmsOnlyExactSignedSubscriptionURL(t *testing.T) {
	privateKey, certificatePEM := testCertificate(t)
	var confirmations atomic.Int32
	verifier := testVerifier(t, certificatePEM, nil, &confirmations)
	message := envelope{
		Type: "SubscriptionConfirmation", MessageID: "subscription", TopicARN: testTopic,
		Message: "confirm", Timestamp: "2025-09-07T12:00:01Z", Token: "opaque-token",
		SubscribeURL:     "https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription&TopicArn=arn%3Aaws%3Asns%3Aus-east-1%3A123456789012%3Amendry-alarms&Token=opaque-token",
		SignatureVersion: "2", SigningCertURL: testCert,
	}
	signEnvelope(t, privateKey, &message)
	raw, _ := json.Marshal(message)
	verified, err := verifier.Verify(context.Background(), string(raw), testTopic)
	if err != nil || verified.Type != application.AWSMessageSubscriptionConfirmation || confirmations.Load() != 1 {
		t.Fatalf("subscription result = %#v confirmations=%d error=%v", verified, confirmations.Load(), err)
	}

	message.SubscribeURL = "https://example.com/?Action=ConfirmSubscription&TopicArn=" + testTopic + "&Token=opaque-token"
	signEnvelope(t, privateKey, &message)
	raw, _ = json.Marshal(message)
	if _, err := verifier.Verify(context.Background(), string(raw), testTopic); !errors.Is(err, application.ErrForbiddenAWSSNS) {
		t.Fatalf("untrusted confirmation error = %v", err)
	}
	if confirmations.Load() != 1 {
		t.Fatalf("unexpected confirmation request count = %d", confirmations.Load())
	}

	message.SubscribeURL = "https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription&TopicArn=arn%3Aaws%3Asns%3Aus-east-1%3A123456789012%3Amendry-alarms&Token=opaque-token&extra=value"
	signEnvelope(t, privateKey, &message)
	raw, _ = json.Marshal(message)
	if _, err := verifier.Verify(context.Background(), string(raw), testTopic); !errors.Is(err, application.ErrForbiddenAWSSNS) {
		t.Fatalf("extra confirmation query error = %v", err)
	}
}

func TestVerifierValidatesUnsubscribeControlWithoutCallingCapability(t *testing.T) {
	privateKey, certificatePEM := testCertificate(t)
	var confirmations atomic.Int32
	verifier := testVerifier(t, certificatePEM, nil, &confirmations)
	message := envelope{
		Type: "UnsubscribeConfirmation", MessageID: "unsubscribe", TopicARN: testTopic,
		Message: "subscription removed", Timestamp: "2025-09-07T12:00:01Z", Token: "opaque-token",
		SubscribeURL:     "https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription&TopicArn=arn%3Aaws%3Asns%3Aus-east-1%3A123456789012%3Amendry-alarms&Token=opaque-token",
		SignatureVersion: "2", SigningCertURL: testCert,
	}
	signEnvelope(t, privateKey, &message)
	raw, _ := json.Marshal(message)
	verified, err := verifier.Verify(context.Background(), string(raw), testTopic)
	if err != nil || verified.Type != application.AWSMessageUnsubscribeConfirmation || confirmations.Load() != 0 {
		t.Fatalf("unsubscribe result = %#v confirmations=%d error=%v", verified, confirmations.Load(), err)
	}

	message.SubscribeURL = "https://example.com/?Action=ConfirmSubscription&TopicArn=" + testTopic + "&Token=opaque-token"
	signEnvelope(t, privateKey, &message)
	raw, _ = json.Marshal(message)
	if _, err := verifier.Verify(context.Background(), string(raw), testTopic); !errors.Is(err, application.ErrForbiddenAWSSNS) {
		t.Fatalf("untrusted unsubscribe URL error = %v", err)
	}
}

func TestVerifierRejectsUntrustedCertificateURLsAndRedirects(t *testing.T) {
	urls := []string{
		"http://sns.us-east-1.amazonaws.com/SimpleNotificationService-test.pem",
		"https://sns.us-east-1.amazonaws.com.evil.example/SimpleNotificationService-test.pem",
		"https://sns.us-east-1.amazonaws.com:444/SimpleNotificationService-test.pem",
		"https://user@sns.us-east-1.amazonaws.com/SimpleNotificationService-test.pem",
		"https://sns.us-east-1.amazonaws.com/other.pem",
		"https://sns.us-east-1.amazonaws.com/SimpleNotificationService-test.pem?next=evil",
		"https://sns.us-east-1.amazonaws.com/SimpleNotificationService-test.pem#fragment",
	}
	for _, certificateURL := range urls {
		verifier, _ := NewVerifier(Options{})
		message := envelope{Type: "Notification", MessageID: "message", TopicARN: testTopic, Message: `{}`, Timestamp: "2025-09-07T12:00:01Z", SignatureVersion: "2", Signature: "eA==", SigningCertURL: certificateURL}
		raw, _ := json.Marshal(message)
		if _, err := verifier.Verify(context.Background(), string(raw), testTopic); !errors.Is(err, application.ErrForbiddenAWSSNS) {
			t.Fatalf("certificate URL %q error = %v", certificateURL, err)
		}
	}

	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://example.com/cert.pem"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
	})}
	verifier, _ := NewVerifier(Options{HTTPClient: client})
	message := envelope{Type: "Notification", MessageID: "message", TopicARN: testTopic, Message: `{}`, Timestamp: "2025-09-07T12:00:01Z", SignatureVersion: "2", Signature: "eA==", SigningCertURL: testCert}
	raw, _ := json.Marshal(message)
	if _, err := verifier.Verify(context.Background(), string(raw), testTopic); !errors.Is(err, application.ErrForbiddenAWSSNS) {
		t.Fatalf("redirect error = %v", err)
	}
}

func TestVerifierRejectsArbitrarySNSPublicationAndAlarmIdentityMismatch(t *testing.T) {
	privateKey, certificatePEM := testCertificate(t)
	verifier := testVerifier(t, certificatePEM, nil, nil)
	for _, payload := range []string{
		`{"event":"not-cloudwatch"}`,
		`{"AlarmName":"other-region","AlarmArn":"arn:aws:cloudwatch:us-west-2:123456789012:alarm:other-region","NewStateValue":"ALARM","StateChangeTime":"2025-09-07T12:00:00Z"}`,
		`{"AlarmName":"unknown-state","AlarmArn":"arn:aws:cloudwatch:us-east-1:123456789012:alarm:unknown-state","NewStateValue":"PENDING","StateChangeTime":"2025-09-07T12:00:00Z"}`,
	} {
		message := envelope{Type: "Notification", MessageID: "message", TopicARN: testTopic, Message: payload,
			Timestamp: "2025-09-07T12:00:01Z", SignatureVersion: "2", SigningCertURL: testCert}
		signEnvelope(t, privateKey, &message)
		raw, _ := json.Marshal(message)
		if _, err := verifier.Verify(context.Background(), string(raw), testTopic); !errors.Is(err, application.ErrInvalidAWSSNS) {
			t.Fatalf("payload %s error = %v", payload, err)
		}
	}
}

func testVerifier(t *testing.T, certificate []byte, fetches, confirmations *atomic.Int32) *Verifier {
	t.Helper()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := []byte("confirmed")
		if certificatePath.MatchString(request.URL.Path) {
			body = certificate
			if fetches != nil {
				fetches.Add(1)
			}
		} else if confirmations != nil {
			confirmations.Add(1)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header), Request: request}, nil
	})}
	verifier, err := NewVerifier(Options{HTTPClient: client, Now: func() time.Time { return time.Date(2025, 9, 7, 12, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func testCertificate(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, 9, 7, 12, 0, 0, 0, time.UTC)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "sns.us-east-1.amazonaws.com"},
		DNSNames: []string{"sns.us-east-1.amazonaws.com"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func signEnvelope(t *testing.T, privateKey *rsa.PrivateKey, message *envelope) {
	t.Helper()
	canonical, err := canonicalMessage(*message)
	if err != nil {
		t.Fatal(err)
	}
	var signature []byte
	if message.SignatureVersion == "1" {
		digest := sha1.Sum(canonical) // #nosec G401 -- SNS v1 fixture.
		signature, err = rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA1, digest[:])
	} else {
		digest := sha256.Sum256(canonical)
		signature, err = rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	}
	if err != nil {
		t.Fatal(err)
	}
	message.Signature = base64.StdEncoding.EncodeToString(signature)
}
