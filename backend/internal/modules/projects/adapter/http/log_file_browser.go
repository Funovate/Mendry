package http

import (
	"context"
	"errors"
	nethttp "net/http"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/platform/httpserver"
)

type logFileBrowserService interface {
	ProbeSSHLogFiles(context.Context, authdomain.User, string, application.LogFileBrowseRequest) (application.LogFileListing, error)
}

func (h *Handler) probeSSHLogFiles(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload struct {
		Host               string `json:"host"`
		Port               int    `json:"port"`
		User               string `json:"user"`
		CredentialSecretID string `json:"credentialSecretId"`
		Path               string `json:"path"`
	}
	if !decodeJSON(writer, request, &payload) {
		return
	}
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	browser, ok := h.service.(logFileBrowserService)
	if !ok {
		writeLogFileBrowseError(writer, request, application.ErrLogFileBrowseUnavailable)
		return
	}
	listing, err := browser.ProbeSSHLogFiles(request.Context(), principal, request.PathValue("projectKey"), application.LogFileBrowseRequest{
		ContainerProbeRequest: application.ContainerProbeRequest{Host: payload.Host, Port: payload.Port, User: payload.User, CredentialSecretID: payload.CredentialSecretID}, Path: payload.Path,
	})
	if err != nil {
		writeLogFileBrowseError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, listing)
}

func writeLogFileBrowseError(writer nethttp.ResponseWriter, request *nethttp.Request, err error) {
	switch {
	case errors.Is(err, application.ErrLogDirectoryNotFound):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusUnprocessableEntity, Code: "log_directory_not_found", Message: "The remote path does not exist or is not a directory or regular file. Enter a directory on the SSH host."})
	case errors.Is(err, application.ErrLogDirectoryNotReadable):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusUnprocessableEntity, Code: "log_directory_not_readable", Message: "The SSH user cannot browse this directory. Check the directory permissions or choose another path."})
	case errors.Is(err, application.ErrLogFileBrowseUnavailable):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusBadGateway, Code: "log_file_browse_unavailable", Message: "Could not browse remote log files. Check the SSH connection and credentials, and ensure Python 3 is installed on the host."})
	default:
		writeApplicationError(writer, request, err)
	}
}
