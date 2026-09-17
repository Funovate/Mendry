package changerequest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

func TestFindChangeRequestReusesExistingRequestAfterCreateResponseLoss(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Method == http.MethodGet && request.URL.Path == "/repos/team/app" {
			_, _ = writer.Write([]byte(`{"full_name":"team/app"}`))
			return
		}
		if request.Method == http.MethodGet && request.URL.Path == "/repos/team/app/pulls" {
			_, _ = writer.Write([]byte(`[{
				"number":12,
				"html_url":"` + "http://" + request.Host + `/team/app/pull/12",
				"draft":true,
				"head":{"ref":"hotfix/remediation/incident-1/run-1","repo":{"full_name":"team/app"}},
				"base":{"ref":"main"}
			}]`))
			return
		}
		t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		http.NotFound(writer, request)
	}))
	defer server.Close()
	client, err := NewClient(ClientOptions{
		HTTPClient: server.Client(), Credentials: tokenResolverStub{projectID: "project-1", secretID: "api-secret", version: 2, token: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := domain.PublicationSnapshot{
		SCMProvider: "github", RemoteURL: "https://127.0.0.1/team/app.git", APIBaseURL: server.URL,
		APICredentialSecretID: "api-secret", APICredentialVersion: 2,
	}
	capabilities, err := client.ProbeCapabilities(context.Background(), "project-1", snapshot)
	if err != nil || capabilities.Status != domain.CapabilitySupported {
		t.Fatalf("ProbeCapabilities() = %+v, %v", capabilities, err)
	}
	found, err := client.FindChangeRequest(context.Background(), domain.ChangeRequestLookup{
		ProjectID: "project-1", RepositoryID: capabilities.RepositoryID,
		SourceBranch: testSourceBranch, TargetBranch: testTargetBranch, PublicationSnapshot: snapshot,
	})
	if err != nil || found.Reference != "#12" || !found.AlreadyExists || !found.Draft {
		t.Fatalf("FindChangeRequest() = %+v, %v", found, err)
	}
}
