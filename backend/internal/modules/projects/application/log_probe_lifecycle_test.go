package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/projects/domain"
)

type recordingLogProbeManager struct {
	statusRequest, uninstallRequest LogProbeRequest
}

func (*recordingLogProbeManager) Install(context.Context, LogProbeRequest) (LogProbeStatus, error) {
	return LogProbeStatus{}, errors.New("unexpected install")
}
func (m *recordingLogProbeManager) Status(_ context.Context, request LogProbeRequest) (LogProbeStatus, error) {
	m.statusRequest = request
	return LogProbeStatus{State: "active"}, nil
}
func (m *recordingLogProbeManager) Uninstall(_ context.Context, request LogProbeRequest) (LogProbeStatus, error) {
	m.uninstallRequest = request
	return LogProbeStatus{State: "not_installed"}, nil
}

func TestLogProbeCanBeRemovedAfterTriggerSwitch(t *testing.T) {
	secretID := "019ff544-405c-7d24-9f10-cb3fc579605c"
	repository := &fakeRepository{
		project: domain.Project{ID: projectID, Key: "payments"},
		configuration: domain.Configuration{
			Source: domain.Source{
				ID: "019ff544-405c-7d31-9f10-cb3fc579605c", Kind: "ssh", CredentialSecretID: &secretID,
				Config: json.RawMessage(`{"schemaVersion":2,"host":"logs.example.test","port":22,"user":"collector","projectFolder":"/srv/app","logPath":"/var/log/app.log","mode":"tail","deployment":{"kind":"host"}}`),
			},
			Trigger: domain.Trigger{ID: "019ff544-405c-7d32-9f10-cb3fc579605c", Kind: "signed_webhook", Version: 2},
		},
	}
	service := newService(t, repository, &fakeCipher{})
	manager := &recordingLogProbeManager{}
	service.logProbes = manager
	user := authdomain.User{ID: userID, Enabled: true}
	if _, err := service.GetLogProbeStatus(context.Background(), user, "payments"); err != nil {
		t.Fatalf("GetLogProbeStatus() = %v", err)
	}
	if _, err := service.UninstallLogProbe(context.Background(), user, "payments"); err != nil {
		t.Fatalf("UninstallLogProbe() = %v", err)
	}
	if manager.statusRequest.Source.Host != "logs.example.test" || manager.uninstallRequest.CredentialSecretID != secretID || manager.uninstallRequest.InboundURL != "" {
		t.Fatalf("unexpected probe target: status=%#v uninstall=%#v", manager.statusRequest, manager.uninstallRequest)
	}
	if _, err := service.InstallLogProbe(context.Background(), user, "payments"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("InstallLogProbe() = %v, want invalid input", err)
	}
}
