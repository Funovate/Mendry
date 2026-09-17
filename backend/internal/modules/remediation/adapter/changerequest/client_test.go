package changerequest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

const (
	testSourceBranch = "hotfix/remediation/incident-1/run-1"
	testTargetBranch = "main"
)

type tokenResolverStub struct {
	projectID string
	secretID  string
	version   int64
	token     string
}

func (r tokenResolverStub) ResolveAPICredential(_ context.Context, projectID, secretID string, version int64) ([]byte, error) {
	if projectID != r.projectID || secretID != r.secretID || version != r.version {
		return nil, fmt.Errorf("credential snapshot mismatch")
	}
	return []byte(r.token), nil
}

func TestProviderClientsProbeFindAndCreate(t *testing.T) {
	tests := []struct {
		provider   string
		apiPath    string
		probePath  string
		findPath   string
		authHeader string
		authValue  string
		draft      bool
	}{
		{
			provider: "github", probePath: "/repos/team/app", findPath: "/repos/team/app/pulls",
			authHeader: "Authorization", authValue: "Bearer test-token", draft: true,
		},
		{
			provider: "gitlab", apiPath: "/api/v4", probePath: "/api/v4/projects/team%2Fapp", findPath: "/api/v4/projects/team%2Fapp/merge_requests",
			authHeader: "PRIVATE-TOKEN", authValue: "test-token", draft: true,
		},
		{
			provider: "gitee", apiPath: "/api/v5", probePath: "/api/v5/repos/team/app", findPath: "/api/v5/repos/team/app/pulls",
			authHeader: "Authorization", authValue: "token test-token", draft: false,
		},
	}
	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get(test.authHeader) != test.authValue {
					t.Errorf("%s = %q", test.authHeader, request.Header.Get(test.authHeader))
				}
				if request.Method == http.MethodGet && request.URL.EscapedPath() == test.probePath {
					writer.WriteHeader(http.StatusOK)
					_, _ = writer.Write([]byte(`{"id":12,"full_name":"team/app"}`))
					return
				}
				if request.Method == http.MethodGet && request.URL.EscapedPath() == test.findPath {
					if request.URL.Query().Get("state") != "all" {
						t.Errorf("state query = %q", request.URL.Query().Get("state"))
					}
					_, _ = writer.Write([]byte(`[]`))
					return
				}
				if request.Method == http.MethodPost {
					if request.Header.Get("Idempotency-Key") != "change:run-1" {
						t.Errorf("Idempotency-Key = %q", request.Header.Get("Idempotency-Key"))
					}
					var body map[string]any
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						t.Errorf("decode create body: %v", err)
					}
					if test.provider == "gitlab" {
						if body["source_branch"] != testSourceBranch || body["target_branch"] != testTargetBranch || body["draft"] != test.draft {
							t.Errorf("GitLab request body = %#v", body)
						}
					} else {
						if body["head"] != "team:"+testSourceBranch || body["base"] != testTargetBranch {
							t.Errorf("%s request body = %#v", test.provider, body)
						}
						if test.provider == "github" && body["draft"] != test.draft {
							t.Errorf("GitHub draft = %#v", body["draft"])
						}
					}
					writer.Header().Set("Content-Type", "application/json")
					_, _ = writer.Write(createdResponse(test.provider, request.Host))
					return
				}
				t.Errorf("unexpected provider request %s %s (escaped %s)", request.Method, request.URL.Path, request.URL.EscapedPath())
				http.NotFound(writer, request)
			}))
			defer server.Close()
			client, err := NewClient(ClientOptions{
				HTTPClient: server.Client(), Credentials: tokenResolverStub{projectID: "project-1", secretID: "api-secret", version: 7, token: "test-token"},
			})
			if err != nil {
				t.Fatal(err)
			}
			snapshot := domain.PublicationSnapshot{
				SCMProvider: test.provider, RemoteURL: "https://127.0.0.1/team/app.git", APIBaseURL: server.URL + test.apiPath,
				APICredentialSecretID: "api-secret", APICredentialVersion: 7,
			}
			capabilities, err := client.ProbeCapabilities(context.Background(), "project-1", snapshot)
			if err != nil || capabilities.Status != domain.CapabilitySupported || capabilities.DraftSupported != test.draft || capabilities.RepositoryID != "team/app" {
				t.Fatalf("ProbeCapabilities() = %+v, %v", capabilities, err)
			}
			lookup := domain.ChangeRequestLookup{
				ProjectID: "project-1", RepositoryID: capabilities.RepositoryID,
				SourceBranch: testSourceBranch, TargetBranch: testTargetBranch, PublicationSnapshot: snapshot,
			}
			found, err := client.FindChangeRequest(context.Background(), lookup)
			if err != nil || found.Reference != "" || found.Status != domain.CapabilitySupported {
				t.Fatalf("FindChangeRequest() = %+v, %v", found, err)
			}
			created, err := client.CreateChangeRequest(context.Background(), domain.ChangeRequestRequest{
				ChangeRequestLookup: lookup, Title: "Hotfix", Body: "Incident summary", Draft: capabilities.DraftSupported,
				CommitHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "change:run-1",
			})
			if err != nil || created.Reference != "#12" || created.Draft != test.draft || created.AlreadyExists {
				t.Fatalf("CreateChangeRequest() = %+v, %v", created, err)
			}
		})
	}
}

func TestCapabilityProbeKeepsUnknownDistinctFromUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.NotFound(writer, request)
	}))
	defer server.Close()
	client, err := NewClient(ClientOptions{
		HTTPClient: server.Client(), Credentials: tokenResolverStub{projectID: "project-1", secretID: "api-secret", version: 1, token: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := client.ProbeCapabilities(context.Background(), "project-1", domain.PublicationSnapshot{
		SCMProvider: "github", RemoteURL: "https://github.com/team/app.git", APIBaseURL: server.URL,
		APICredentialSecretID: "api-secret", APICredentialVersion: 1,
	})
	if err != nil || unknown.Status != domain.CapabilityUnknown || unknown.Status == domain.CapabilityUnsupported {
		t.Fatalf("ambiguous 404 capability = %+v, %v", unknown, err)
	}
	unsupported, err := client.ProbeCapabilities(context.Background(), "project-1", domain.PublicationSnapshot{SCMProvider: "generic"})
	if err != nil || unsupported.Status != domain.CapabilityUnsupported {
		t.Fatalf("generic capability = %+v, %v", unsupported, err)
	}
	yunxiao, err := client.ProbeCapabilities(context.Background(), "project-1", domain.PublicationSnapshot{SCMProvider: "yunxiao"})
	if err != nil || yunxiao.Status != domain.CapabilityUnsupported {
		t.Fatalf("Yunxiao capability = %+v, %v", yunxiao, err)
	}
}

func TestGitLabProjectPathIsEscapedAsOneSegment(t *testing.T) {
	base, err := parseAPIBase("https://gitlab.example.com/api/v4")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := repositoryEndpoint(base, "group/subgroup/app", "gitlab", "merge_requests")
	if endpoint.EscapedPath() != "/api/v4/projects/group%2Fsubgroup%2Fapp/merge_requests" {
		t.Fatalf("escaped endpoint = %s", endpoint.EscapedPath())
	}
}

func createdResponse(provider, host string) []byte {
	hostURL := "http://" + host
	switch provider {
	case "github":
		payload, _ := json.Marshal(map[string]any{
			"number": 12, "html_url": hostURL + "/team/app/pull/12", "draft": true,
			"head": map[string]any{"ref": testSourceBranch, "repo": map[string]any{"full_name": "team/app"}},
			"base": map[string]any{"ref": testTargetBranch},
		})
		return payload
	case "gitlab":
		payload, _ := json.Marshal(map[string]any{
			"iid": 12, "web_url": hostURL + "/team/app/-/merge_requests/12", "draft": true,
			"source_branch": testSourceBranch, "target_branch": testTargetBranch,
		})
		return payload
	default:
		payload, _ := json.Marshal(map[string]any{
			"number": 12, "html_url": hostURL + "/team/app/pulls/12",
			"head": map[string]any{"ref": testSourceBranch}, "base": map[string]any{"ref": testTargetBranch},
		})
		return payload
	}
}
