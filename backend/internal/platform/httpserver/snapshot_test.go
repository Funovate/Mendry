package httpserver

import (
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestBuildBodySnapshotRedactsNestedSensitiveFields(t *testing.T) {
	raw := []byte(`{"username":"alice","password":"hunter2","profile":{"authToken":"abc","name":"Alice"}}`)
	snapshot := buildBodySnapshot(raw)

	if !snapshot.present || snapshot.parseError || snapshot.truncated {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(snapshot.body), &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body = %q", err, snapshot.body)
	}
	if decoded["password"] != redactedPlaceholder {
		t.Fatalf("password = %#v", decoded["password"])
	}
	if decoded["username"] != "alice" {
		t.Fatalf("username = %#v", decoded["username"])
	}
	profile, ok := decoded["profile"].(map[string]any)
	if !ok {
		t.Fatalf("profile = %#v", decoded["profile"])
	}
	if profile["authToken"] != redactedPlaceholder {
		t.Fatalf("authToken = %#v", profile["authToken"])
	}
	if profile["name"] != "Alice" {
		t.Fatalf("name = %#v", profile["name"])
	}
}

func TestBuildBodySnapshotRedactsArrayElements(t *testing.T) {
	raw := []byte(`[{"secret":"s1"},{"secret":"s2","note":"ok"}]`)
	snapshot := buildBodySnapshot(raw)

	var decoded []map[string]any
	if err := json.Unmarshal([]byte(snapshot.body), &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body = %q", err, snapshot.body)
	}
	for _, item := range decoded {
		if item["secret"] != redactedPlaceholder {
			t.Fatalf("secret = %#v", item["secret"])
		}
	}
	if decoded[1]["note"] != "ok" {
		t.Fatalf("note = %#v", decoded[1]["note"])
	}
}

func TestBuildBodySnapshotFlagsInvalidJSON(t *testing.T) {
	snapshot := buildBodySnapshot([]byte("not json"))
	if !snapshot.present || !snapshot.parseError {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.body != "" {
		t.Fatalf("body = %q, want empty on parse error", snapshot.body)
	}
}

func TestBuildBodySnapshotEmptyWhenNoBytes(t *testing.T) {
	snapshot := buildBodySnapshot(nil)
	if snapshot.present {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestBuildBodySnapshotTruncatesLongPayload(t *testing.T) {
	value := strings.Repeat("a", snapshotMaxBytes*2)
	raw, err := json.Marshal(map[string]string{"note": value})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	snapshot := buildBodySnapshot(raw)
	if !snapshot.truncated {
		t.Fatalf("truncated = %v, want true", snapshot.truncated)
	}
	if len(snapshot.body) > snapshotMaxBytes {
		t.Fatalf("len(body) = %d, want <= %d", len(snapshot.body), snapshotMaxBytes)
	}
}

func TestBuildBodySnapshotRedactsSecretValueAndHyphenatedAPIKey(t *testing.T) {
	raw := []byte(`{"name":"prod-ssh","kind":"ssh_private_key","value":"super-secret-material","api-key":"hyphenated"}`)
	snapshot := buildBodySnapshot(raw)

	var decoded map[string]any
	if err := json.Unmarshal([]byte(snapshot.body), &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body = %q", err, snapshot.body)
	}
	if decoded["value"] != redactedPlaceholder {
		t.Fatalf("value = %#v", decoded["value"])
	}
	if decoded["api-key"] != redactedPlaceholder {
		t.Fatalf("api-key = %#v", decoded["api-key"])
	}
	if decoded["name"] != "prod-ssh" {
		t.Fatalf("name = %#v", decoded["name"])
	}
	if strings.Contains(snapshot.body, "super-secret-material") || strings.Contains(snapshot.body, "hyphenated") {
		t.Fatalf("plaintext leaked: %s", snapshot.body)
	}
}

func TestBuildQuerySnapshotRedactsSensitiveParams(t *testing.T) {
	values := url.Values{"token": {"abc", "def"}, "page": {"2"}}
	snapshot := buildQuerySnapshot(values)
	if !snapshot.present {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	var decoded map[string][]string
	if err := json.Unmarshal([]byte(snapshot.query), &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; query = %q", err, snapshot.query)
	}
	if len(decoded["token"]) != 2 || decoded["token"][0] != redactedPlaceholder || decoded["token"][1] != redactedPlaceholder {
		t.Fatalf("token = %#v", decoded["token"])
	}
	if decoded["page"][0] != "2" {
		t.Fatalf("page = %#v", decoded["page"])
	}
}

func TestBuildQuerySnapshotEmptyWhenNoParams(t *testing.T) {
	snapshot := buildQuerySnapshot(url.Values{})
	if snapshot.present {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestBuildQuerySnapshotTruncatesLongPayload(t *testing.T) {
	values := url.Values{"note": {strings.Repeat("a", snapshotMaxBytes*2)}}
	snapshot := buildQuerySnapshot(values)
	if !snapshot.present || !snapshot.truncated {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if len(snapshot.query) > snapshotMaxBytes {
		t.Fatalf("len(query) = %d, want <= %d", len(snapshot.query), snapshotMaxBytes)
	}
}

func TestTruncateUTF8CutsOnRuneBoundary(t *testing.T) {
	// "世" is 3 bytes; cutting at 5 must keep the complete first CJK rune and
	// drop the incomplete second one instead of leaving a continuation byte.
	truncated, ok := truncateUTF8("aa世界", 5)
	if !ok || truncated != "aa世" {
		t.Fatalf("truncateUTF8() = %q %v", truncated, ok)
	}
}

func TestBodyCaptureStopsBufferingAtLimit(t *testing.T) {
	capture := newBodyCapture(io.NopCloser(strings.NewReader("abcdefghij")), 4)
	got, err := io.ReadAll(capture)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(got) != "abcdefghij" {
		t.Fatalf("downstream bytes = %q", got)
	}
	if string(capture.bytes()) != "abcd" {
		t.Fatalf("captured bytes = %q", capture.bytes())
	}
}
