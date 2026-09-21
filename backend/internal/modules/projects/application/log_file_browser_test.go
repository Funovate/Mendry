package application

import (
	"context"
	"errors"
	"testing"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/projects/domain"
)

type recordingLogFileBrowser struct {
	request LogFileBrowseRequest
	calls   int
	err     error
}

func (b *recordingLogFileBrowser) ListLogFiles(_ context.Context, request LogFileBrowseRequest) (LogFileListing, error) {
	b.request = request
	b.calls++
	return LogFileListing{Directory: request.Path}, b.err
}

type scopedLogFileRepository struct {
	*fakeRepository
	secretProjectID string
}

func (r *scopedLogFileRepository) GetEncryptedSecret(ctx context.Context, projectID, secretID string) (domain.EncryptedSecret, error) {
	r.secretProjectID = projectID
	return r.fakeRepository.GetEncryptedSecret(ctx, projectID, secretID)
}

func TestProbeSSHLogFilesValidatesBeforeRemoteAccess(t *testing.T) {
	const secretID = "019ff544-405c-7d24-9f10-cb3fc579605c"
	for _, tc := range []struct {
		name    string
		enabled bool
		path    string
		kind    domain.SecretKind
		want    error
	}{
		{"valid", true, "/var/log", domain.SecretSSHPrivateKey, nil},
		{"disabled user", false, "/var/log", domain.SecretSSHPrivateKey, ErrForbidden},
		{"relative path", true, "var/log", domain.SecretSSHPrivateKey, ErrInvalidInput},
		{"control character", true, "/var/log\x00", domain.SecretSSHPrivateKey, ErrInvalidInput},
		{"wrong credential", true, "/var/log", domain.SecretHTTPBearer, ErrInvalidInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &scopedLogFileRepository{fakeRepository: &fakeRepository{project: domain.Project{ID: projectID, Key: "demo"}, secret: domain.EncryptedSecret{Secret: domain.Secret{ID: secretID, Kind: tc.kind}}}}
			service := newService(t, repo, &fakeCipher{})
			browser := &recordingLogFileBrowser{}
			service.logFiles = browser
			listing, err := service.ProbeSSHLogFiles(context.Background(), authdomain.User{ID: userID, Enabled: tc.enabled}, "demo", LogFileBrowseRequest{ContainerProbeRequest: ContainerProbeRequest{ProjectID: "untrusted", Host: "logs.example.com", Port: 22, User: "collector", CredentialSecretID: secretID}, Path: tc.path})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
			if tc.want != nil {
				if browser.calls != 0 {
					t.Fatal("invalid input reached remote host")
				}
				return
			}
			if browser.calls != 1 || browser.request.ProjectID != projectID || repo.secretProjectID != projectID || listing.Entries == nil {
				t.Fatalf("project scope/empty list lost: %+v %+v", browser, listing)
			}
		})
	}
}

func TestProbeSSHLogFilesPreservesKnownErrorsAndHidesDiagnostics(t *testing.T) {
	const secretID = "019ff544-405c-7d24-9f10-cb3fc579605c"
	repo := &fakeRepository{project: domain.Project{ID: projectID, Key: "demo"}, secret: domain.EncryptedSecret{Secret: domain.Secret{ID: secretID, Kind: domain.SecretSSHPrivateKey}}}
	service := newService(t, repo, &fakeCipher{})
	for _, remoteErr := range []error{ErrLogDirectoryNotFound, ErrLogDirectoryNotReadable, errors.New("private SSH diagnostics")} {
		service.logFiles = &recordingLogFileBrowser{err: remoteErr}
		_, err := service.ProbeSSHLogFiles(context.Background(), authdomain.User{ID: userID, Enabled: true}, "demo", LogFileBrowseRequest{ContainerProbeRequest: ContainerProbeRequest{Host: "logs.example.com", Port: 22, User: "collector", CredentialSecretID: secretID}, Path: "/var/log"})
		want := remoteErr
		if remoteErr != ErrLogDirectoryNotFound && remoteErr != ErrLogDirectoryNotReadable {
			want = ErrLogFileBrowseUnavailable
		}
		if !errors.Is(err, want) {
			t.Fatalf("error=%v want=%v", err, want)
		}
	}
}
