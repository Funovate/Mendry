package application

import (
	"context"
	"errors"
	"strings"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/projects/domain"
)

var (
	ErrLogFileBrowseUnavailable = errors.New("remote log file browser is unavailable")
	ErrLogDirectoryNotFound     = errors.New("remote log directory does not exist")
	ErrLogDirectoryNotReadable  = errors.New("remote log directory cannot be read")
)

type LogFileBrowseRequest struct {
	ContainerProbeRequest
	Path string
}

type RemoteLogEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Readable bool   `json:"readable"`
}

type LogFileListing struct {
	Directory string           `json:"directory"`
	Entries   []RemoteLogEntry `json:"entries"`
	Truncated bool             `json:"truncated"`
}

type LogFileBrowserPort interface {
	ListLogFiles(context.Context, LogFileBrowseRequest) (LogFileListing, error)
}

// ValidLogBrowsePath accepts only absolute host paths; no shell expansion is performed.
func ValidLogBrowsePath(value string) bool {
	return strings.HasPrefix(value, "/") && len(value) <= 4096 && !strings.ContainsAny(value, "\x00\r\n")
}

func (s *Service) ProbeSSHLogFiles(ctx context.Context, principal authdomain.User, projectKey string, input LogFileBrowseRequest) (LogFileListing, error) {
	project, err := s.resolveProject(ctx, principal, projectKey)
	if err != nil {
		return LogFileListing{}, err
	}
	if err := domain.ValidateSSHContainerProbe(input.Host, input.Port, input.User, input.CredentialSecretID); err != nil {
		return LogFileListing{}, ErrInvalidInput
	}
	if !ValidLogBrowsePath(input.Path) {
		return LogFileListing{}, ErrInvalidInput
	}
	encrypted, err := s.repository.GetEncryptedSecret(ctx, project.ID, input.CredentialSecretID)
	if err != nil {
		return LogFileListing{}, ErrLogFileBrowseUnavailable
	}
	if encrypted.Kind != domain.SecretSSHPrivateKey {
		return LogFileListing{}, ErrInvalidInput
	}
	if s.logFiles == nil {
		return LogFileListing{}, ErrLogFileBrowseUnavailable
	}
	input.ProjectID = project.ID
	input.Host, input.User = strings.TrimSpace(input.Host), strings.TrimSpace(input.User)
	listing, err := s.logFiles.ListLogFiles(ctx, input)
	if err != nil {
		if errors.Is(err, ErrLogDirectoryNotFound) || errors.Is(err, ErrLogDirectoryNotReadable) {
			return LogFileListing{}, err
		}
		return LogFileListing{}, ErrLogFileBrowseUnavailable
	}
	if listing.Entries == nil {
		listing.Entries = []RemoteLogEntry{}
	}
	return listing, nil
}
