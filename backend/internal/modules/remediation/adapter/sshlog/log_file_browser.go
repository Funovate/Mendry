package sshlog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/platform/observability"
)

var _ application.LogFileBrowserPort = (*ContainerProbe)(nil)

// ListLogFiles uses the same authenticated transport as container discovery.
// Paths travel as encoded data, never as user-authored commands. No sudo is used:
// readability reflects the SSH account that will run the managed probe.
func (p *ContainerProbe) ListLogFiles(ctx context.Context, request application.LogFileBrowseRequest) (listing application.LogFileListing, err error) {
	if err := domain.ValidateSSHContainerProbe(request.Host, request.Port, request.User, request.CredentialSecretID); err != nil {
		return listing, err
	}
	if !application.ValidLogBrowsePath(request.Path) {
		return listing, application.ErrInvalidInput
	}
	started := time.Now()
	outputBytes := 0
	defer func() {
		observability.LogSSHEvidenceRequest(ctx, p.logger, observability.SSHEvidenceRequest{
			Operation: "log_file_inventory", Command: "python3 -c <log file inventory>", Host: request.Host, Port: request.Port, Duration: time.Since(started), Bytes: outputBytes,
			CredentialSecretID: request.CredentialSecretID, CredentialKind: string(domain.SecretSSHPrivateKey), Err: err,
		})
	}()
	encrypted, err := p.secrets.GetEncryptedSecret(ctx, request.ProjectID, request.CredentialSecretID)
	if err != nil {
		return listing, fmt.Errorf("load SSH credential: %w", err)
	}
	if encrypted.Kind != domain.SecretSSHPrivateKey {
		return listing, application.ErrInvalidInput
	}
	plaintext, err := p.cipher.Decrypt(request.ProjectID, encrypted.ID, encrypted.Kind, encrypted.Ciphertext, encrypted.Nonce)
	if err != nil {
		return listing, fmt.Errorf("decrypt SSH credential: %w", err)
	}
	defer clearBytes(plaintext)
	command := "python3 -c " + shellQuote(logFileInventoryPython) + " " + shellQuote(base64.StdEncoding.EncodeToString([]byte(request.Path)))
	reader := &Reader{command: p.command, timeout: p.timeout}
	args, cleanup, err := reader.sshArgs(SourceConfig{ProjectID: request.ProjectID, Host: request.Host, Port: request.Port, User: request.User, CredentialSecretID: request.CredentialSecretID, CredentialKind: encrypted.Kind, plaintext: plaintext}, command)
	if err != nil {
		return listing, err
	}
	defer cleanup()
	stdout, stderr, truncated, runErr := runBoundedSSHCommand(ctx, p.command, args, p.timeout, maxContainerInventoryBytes)
	outputBytes = len(stdout) + len(stderr)
	if runErr != nil {
		return listing, probeCommandError(ctx, runErr, stderr)
	}
	if truncated {
		return listing, fmt.Errorf("log file inventory output exceeded limit")
	}
	var response struct {
		application.LogFileListing
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		return listing, fmt.Errorf("invalid log file inventory response")
	}
	switch response.Error {
	case "":
	case "not_found":
		return listing, application.ErrLogDirectoryNotFound
	case "not_readable":
		return listing, application.ErrLogDirectoryNotReadable
	default:
		return listing, application.ErrLogFileBrowseUnavailable
	}
	listing = response.LogFileListing
	if !application.ValidLogBrowsePath(listing.Directory) || len(listing.Entries) > 100 {
		return application.LogFileListing{}, fmt.Errorf("invalid log file inventory")
	}
	seen := make(map[string]bool)
	for _, entry := range listing.Entries {
		if entry.Name == "" || entry.Name == "." || entry.Name == ".." || strings.ContainsAny(entry.Name, "/\x00\r\n") || entry.Path != path.Join(listing.Directory, entry.Name) || (entry.Kind != "file" && entry.Kind != "directory") || seen[entry.Name] {
			return application.LogFileListing{}, fmt.Errorf("invalid log file inventory entry")
		}
		seen[entry.Name] = true
	}
	if listing.Entries == nil {
		listing.Entries = []application.RemoteLogEntry{}
	}
	return listing, nil
}

const logFileInventoryPython = `import base64, json, os, stat, sys
requested = os.path.abspath(base64.b64decode(sys.argv[1]).decode("utf-8"))
try:
    mode = os.stat(requested).st_mode
    if stat.S_ISREG(mode):
        directory = os.path.dirname(requested)
    elif stat.S_ISDIR(mode):
        directory = requested
    else:
        raise NotADirectoryError(requested)
    entries = []
    truncated = False
    with os.scandir(directory) as scan:
        for scanned, entry in enumerate(scan):
            if scanned >= 1000:
                truncated = True
                break
            if any(c in entry.name for c in ("\x00", "\r", "\n")):
                continue
            try:
                if entry.is_dir():
                    kind = "directory"
                    readable = os.access(entry.path, os.R_OK | os.X_OK)
                elif entry.is_file():
                    kind = "file"
                    readable = os.access(entry.path, os.R_OK)
                else:
                    continue
            except OSError:
                continue
            if len(entries) >= 100:
                truncated = True
                break
            entries.append({"name": entry.name, "path": entry.path, "kind": kind, "readable": readable})
    entries.sort(key=lambda e: (e["kind"] != "directory", e["name"].lower(), e["name"]))
    print(json.dumps({"directory": directory, "entries": entries, "truncated": truncated}))
except (FileNotFoundError, NotADirectoryError):
    print(json.dumps({"error": "not_found"}))
except PermissionError:
    print(json.dumps({"error": "not_readable"}))
`
