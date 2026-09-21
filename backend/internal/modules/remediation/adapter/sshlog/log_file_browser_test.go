package sshlog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/modules/remediation/adapter/sshlog"
)

func TestSSHLogFileBrowserUsesEncodedPathAndValidatesResponse(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		want           error
		invalid        bool
	}{
		{name: "valid", response: `{"directory":"/var/log","entries":[{"name":"app.log","path":"/var/log/app.log","kind":"file","readable":true}],"truncated":false}`},
		{name: "missing", response: `{"error":"not_found"}`, want: application.ErrLogDirectoryNotFound},
		{name: "permissions", response: `{"error":"not_readable"}`, want: application.ErrLogDirectoryNotReadable},
		{name: "out of directory", response: `{"directory":"/var/log","entries":[{"name":"app.log","path":"/etc/passwd","kind":"file","readable":true}]}`, invalid: true},
		{name: "malformed", response: `not json`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			command := filepath.Join(dir, "ssh")
			arguments := filepath.Join(dir, "arguments")
			t.Setenv("LOG_FILES_ARGS", arguments)
			t.Setenv("LOG_FILES_RESPONSE", tc.response)
			if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$LOG_FILES_ARGS\"\nprintf '%s' \"$LOG_FILES_RESPONSE\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			browser, err := sshlog.NewContainerProbe(sshlog.ContainerProbeOptions{Secrets: staticSecrets{}, Cipher: staticCipher{}, SSHCommand: command})
			if err != nil {
				t.Fatal(err)
			}
			request := application.LogFileBrowseRequest{ContainerProbeRequest: application.ContainerProbeRequest{ProjectID: testProjectID, Host: "logs.example.com", Port: 22, User: "collector", CredentialSecretID: testSecretID}, Path: "/var/log/'; touch injected; #"}
			listing, err := browser.ListLogFiles(context.Background(), request)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid response accepted")
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
			if tc.name == "valid" && len(listing.Entries) != 1 {
				t.Fatalf("listing=%+v", listing)
			}
			args, readErr := os.ReadFile(arguments)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(string(args), request.Path) || strings.Contains(string(args), "PRIVATE KEY") || strings.Contains(string(args), "sudo") {
				t.Fatalf("path/credentials or privilege escalation in SSH command")
			}
		})
	}
}
