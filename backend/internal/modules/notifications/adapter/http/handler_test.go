package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	authhttp "mendry/backend/internal/modules/auth/adapter/http"
	authapplication "mendry/backend/internal/modules/auth/application"
	authdomain "mendry/backend/internal/modules/auth/domain"
	notificationhttp "mendry/backend/internal/modules/notifications/adapter/http"
	notificationplatform "mendry/backend/internal/modules/notifications/adapter/platform"
	"mendry/backend/internal/modules/notifications/application"
	"mendry/backend/internal/modules/notifications/domain"
	projectsecret "mendry/backend/internal/modules/projects/adapter/secret"
	projectapplication "mendry/backend/internal/modules/projects/application"
	projectdomain "mendry/backend/internal/modules/projects/domain"
)

type authService struct{}

func (authService) Login(context.Context, string, []byte, string) (authapplication.LoginResult, error) {
	return authapplication.LoginResult{}, nil
}
func (authService) Logout(context.Context, string) error { return nil }
func (authService) Authenticate(_ context.Context, token string) (authdomain.User, error) {
	if token == "" {
		return authdomain.User{}, authapplication.ErrUnauthenticated
	}
	return authdomain.User{ID: "user-1", Username: "operator", Enabled: true}, nil
}

type projects struct {
	err    error
	calls  int
	userID string
	id     string
}

func (p *projects) GetProject(_ context.Context, user authdomain.User, _ string) (projectdomain.Project, error) {
	p.calls++
	p.userID = user.ID
	return projectdomain.Project{ID: p.id}, p.err
}

type store struct {
	channel    domain.Channel
	deliveries []domain.Delivery
	calls      int
}

func (s *store) ListChannels(context.Context, string) ([]domain.Channel, error) {
	s.calls++
	return []domain.Channel{s.channel}, nil
}
func (s *store) GetChannel(_ context.Context, projectID, id string) (domain.Channel, error) {
	if projectID != s.channel.ProjectID || id != s.channel.ID {
		return domain.Channel{}, domain.ErrNotFound
	}
	return s.channel, nil
}
func (s *store) SaveChannel(_ context.Context, c domain.Channel, _ bool) (domain.Channel, error) {
	s.channel = c
	s.channel.HasCredentials = true
	return s.channel, nil
}
func (s *store) DeleteChannel(context.Context, string, string) error { return nil }
func (s *store) ListDeliveries(context.Context, string) ([]domain.Delivery, error) {
	if s.deliveries == nil {
		return []domain.Delivery{}, nil
	}
	return s.deliveries, nil
}
func (s *store) RetryDelivery(context.Context, string, string) error { return domain.ErrConflict }
func (s *store) Claim(context.Context) (domain.Delivery, error) {
	return domain.Delivery{}, domain.ErrNotFound
}
func (s *store) Finish(context.Context, domain.Delivery, error) error { return nil }

type sender struct {
	err   error
	calls int
}

func (s *sender) Send(context.Context, string, domain.Credentials, string) error {
	s.calls++
	return s.err
}
func setup(t *testing.T) (*http.ServeMux, *store, *projects, *sender) {
	t.Helper()
	auth, err := authhttp.NewHandler(authhttp.HandlerOptions{Service: authService{}})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := projectsecret.NewAESGCM(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	db := &store{}
	p := &projects{id: uuid.NewString()}
	out := &sender{}
	svc := application.NewService(db, cipher, out, notificationplatform.Validate)
	mux := http.NewServeMux()
	notificationhttp.NewHandler(svc, p, auth).Register(mux)
	return mux, db, p, out
}
func call(mux *http.ServeMux, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		r.AddCookie(&http.Cookie{Name: authhttp.SessionCookieName, Value: "session-token"})
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}
func TestNotificationRoutesRequireAuthenticationAndProjectResolution(t *testing.T) {
	mux, db, p, _ := setup(t)
	w := call(mux, "GET", "/api/v1/projects/payments/notifications/channels", "", false)
	if w.Code != 401 || p.calls != 0 || db.calls != 0 {
		t.Fatalf("unauthenticated read %d", w.Code)
	}
	p.err = projectapplication.ErrNotFound
	w = call(mux, "GET", "/api/v1/projects/payments/notifications/channels", "", true)
	if w.Code != 404 || db.calls != 0 || p.userID != "user-1" {
		t.Fatalf("project resolution not enforced %d", w.Code)
	}
}
func TestChannelCRUDAndSafeListEnvelope(t *testing.T) {
	mux, db, _, out := setup(t)
	w := call(mux, "POST", "/api/v1/projects/payments/notifications/channels", `{"name":"Ops","platform":"telegram","enabled":true,"credentials":{"botToken":"123456:abcdefghijklmnop","chatId":"-100123"}}`, true)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	for _, secret := range []string{"abcdefghijklmnop", "ciphertext", "nonce", "botToken", "chatId"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("secret leaked %s", w.Body)
		}
	}
	id := db.channel.ID
	w = call(mux, "GET", "/api/v1/projects/payments/notifications/channels", "", true)
	var envelope struct {
		Data []domain.Channel `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || w.Code != 200 || len(envelope.Data) != 1 || envelope.Meta.Total != 1 {
		t.Fatalf("list envelope %s %v", w.Body, err)
	}
	original := string(db.channel.Ciphertext)
	w = call(mux, "PUT", "/api/v1/projects/payments/notifications/channels/"+id, `{"name":"Ops renamed","platform":"telegram","enabled":false}`, true)
	if w.Code != 200 || db.channel.Enabled || string(db.channel.Ciphertext) != original {
		t.Fatalf("metadata update %d %s", w.Code, w.Body)
	}
	w = call(mux, "POST", "/api/v1/projects/payments/notifications/channels/"+id+"/test", "", true)
	if w.Code != 200 || out.calls != 1 {
		t.Fatalf("test %d %s", w.Code, w.Body)
	}
	out.err = errors.New("secret transport URL")
	w = call(mux, "POST", "/api/v1/projects/payments/notifications/channels/"+id+"/test", "", true)
	if w.Code != 502 || strings.Contains(w.Body.String(), "secret transport") {
		t.Fatalf("unsafe platform error %d %s", w.Code, w.Body)
	}
	w = call(mux, "DELETE", "/api/v1/projects/payments/notifications/channels/"+id, "", true)
	if w.Code != 204 {
		t.Fatalf("delete %d", w.Code)
	}
}
func TestCancelledDeliveryListStateAndRedaction(t *testing.T) {
	mux, db, _, _ := setup(t)
	db.deliveries = []domain.Delivery{{
		ID: uuid.NewString(), State: domain.DeliveryCancelled,
		Ciphertext: []byte("old-secret-token"), Nonce: []byte("old-secret-nonce"), LeaseToken: "old-lease",
	}}
	w := call(mux, "GET", "/api/v1/projects/payments/notifications/deliveries", "", true)
	var envelope struct {
		Data []domain.Delivery `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || w.Code != 200 || len(envelope.Data) != 1 || envelope.Data[0].State != domain.DeliveryCancelled {
		t.Fatalf("cancelled API state lost: %d %s %v", w.Code, w.Body, err)
	}
	for _, secret := range []string{"old-secret", "old-lease", "ciphertext", "nonce", "leaseToken"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("cancelled delivery audit leaked credential: %s", w.Body)
		}
	}
}

func TestNotificationInvalidInputAndDeliveryRetry(t *testing.T) {
	mux, _, _, _ := setup(t)
	for _, body := range []string{
		`{"name":"Ops","platform":"feishu","enabled":true,"credentials":{"webhookUrl":"https://127.0.0.1/internal"}}`,
		`{"name":"Ops","platform":"telegram","enabled":true}`,
		`{"name":"Ops","platform":"unknown","credentials":{}}`,
		`{"name":"Ops","unexpected":"secret"}`,
	} {
		w := call(mux, "POST", "/api/v1/projects/payments/notifications/channels", body, true)
		if w.Code != 400 {
			t.Fatalf("invalid input accepted %d %s", w.Code, w.Body)
		}
	}
	w := call(mux, "GET", "/api/v1/projects/payments/notifications/deliveries", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("empty history %d %s", w.Code, w.Body)
	}
	w = call(mux, "POST", "/api/v1/projects/payments/notifications/deliveries/"+uuid.NewString()+"/retry", "", true)
	if w.Code != 409 {
		t.Fatalf("conflicting retry %d", w.Code)
	}
	w = call(mux, "POST", "/api/v1/projects/payments/notifications/deliveries/not-a-uuid/retry", "", true)
	if w.Code != 400 {
		t.Fatalf("invalid retry ID %d", w.Code)
	}
}
