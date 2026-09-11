package application

import (
	"bufio"
	"net/url"
	"strings"

	"mendry/backend/internal/modules/projects/domain"
)

type GitRef struct {
	Name   string
	Commit string
}

type RepositoryRefs struct {
	DefaultBranch  string
	DeployedCommit string
	Branches       []GitRef
}

func ParseGitLsRemote(output string) (RepositoryRefs, error) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	heads := make([]GitRef, 0)
	symbolicHEAD := ""
	headCommit := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "ref: ") {
			ref, _, found := strings.Cut(strings.TrimPrefix(line, "ref: "), "\t")
			if !found {
				ref, _, found = strings.Cut(strings.TrimPrefix(line, "ref: "), " ")
			}
			if found && strings.HasPrefix(ref, "refs/heads/") {
				symbolicHEAD = strings.TrimPrefix(ref, "refs/heads/")
			}
			continue
		}
		commit, ref, found := strings.Cut(line, "\t")
		if !found {
			commit, ref, found = strings.Cut(line, " ")
		}
		if !found {
			continue
		}
		commit = strings.TrimSpace(commit)
		ref = strings.TrimSpace(ref)
		if !domain.IsCommitSHA(commit) {
			continue
		}
		if ref == "HEAD" {
			headCommit = commit
			continue
		}
		if strings.HasPrefix(ref, "refs/heads/") {
			heads = append(heads, GitRef{Name: strings.TrimPrefix(ref, "refs/heads/"), Commit: commit})
		}
	}
	if err := scanner.Err(); err != nil || len(heads) == 0 {
		return RepositoryRefs{}, ErrGitUnreachable
	}

	selected := selectDefaultBranch(heads, symbolicHEAD)
	if selected.Name == "" && headCommit != "" {
		for _, branch := range heads {
			if branch.Commit == headCommit {
				selected = branch
				break
			}
		}
	}
	if selected.Name == "" {
		selected = heads[0]
	}
	return RepositoryRefs{DefaultBranch: selected.Name, DeployedCommit: selected.Commit, Branches: heads}, nil
}

func selectDefaultBranch(heads []GitRef, symbolicHEAD string) GitRef {
	if symbolicHEAD != "" {
		for _, branch := range heads {
			if branch.Name == symbolicHEAD {
				return branch
			}
		}
	}
	for _, preferred := range []string{"main", "master"} {
		for _, branch := range heads {
			if branch.Name == preferred {
				return branch
			}
		}
	}
	return GitRef{}
}

func AuthenticatedHTTPSRemote(remoteURL, credential string) (string, error) {
	parsed, err := url.Parse(remoteURL)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" {
		return "", ErrInvalidInput
	}
	username, token := splitHTTPSCredential(credential)
	if token == "" {
		return "", ErrInvalidInput
	}
	if username == "" {
		username = "git"
	}
	parsed.User = url.UserPassword(username, token)
	return parsed.String(), nil
}

func splitHTTPSCredential(value string) (string, string) {
	username, token, found := strings.Cut(value, ":")
	if !found {
		return "", strings.TrimSpace(value)
	}
	return strings.TrimSpace(username), strings.TrimSpace(token)
}
