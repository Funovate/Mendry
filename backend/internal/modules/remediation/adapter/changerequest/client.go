package changerequest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

const (
	defaultTimeout     = 15 * time.Second
	maxResponseBytes   = 1 << 20
	maxCredentialBytes = 64 << 10
)

type APICredentialResolver interface {
	ResolveAPICredential(context.Context, string, string, int64) ([]byte, error)
}

type ClientOptions struct {
	HTTPClient  *http.Client
	Credentials APICredentialResolver
}

type Client struct {
	httpClient  *http.Client
	credentials APICredentialResolver
}

func NewClient(options ClientOptions) (*Client, error) {
	if options.Credentials == nil {
		return nil, fmt.Errorf("change-request credential resolver is required")
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: defaultTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Client{httpClient: client, credentials: options.Credentials}, nil
}

var _ domain.ChangeRequestPort = (*Client)(nil)

func (c *Client) ProbeCapabilities(ctx context.Context, projectID string, snapshot domain.PublicationSnapshot) (domain.ProviderCapabilities, error) {
	provider, repository, _, base, reason := resolveTarget(snapshot)
	if reason != "" {
		status := domain.CapabilityUnknown
		if snapshot.SCMProvider == "generic" || snapshot.SCMProvider == "yunxiao" {
			status = domain.CapabilityUnsupported
		}
		return domain.ProviderCapabilities{Status: status, ReasonCode: reason}, nil
	}
	if snapshot.APICredentialSecretID == "" || snapshot.APICredentialVersion < 1 {
		return domain.ProviderCapabilities{Status: domain.CapabilityUnknown, ReasonCode: "api_credential_missing"}, nil
	}
	url := repositoryEndpoint(base, repository, provider, "")
	_, err := c.requestJSON(ctx, projectID, snapshot, provider, http.MethodGet, url, nil, "")
	if err != nil {
		if ctx.Err() != nil {
			return domain.ProviderCapabilities{}, ctx.Err()
		}
		return domain.ProviderCapabilities{Status: domain.CapabilityUnknown, ReasonCode: capabilityFailureCode(err)}, nil
	}
	return domain.ProviderCapabilities{
		Status: domain.CapabilitySupported, DraftSupported: provider != "gitee",
		RepositoryID: repository, APIBaseURL: base.String(),
	}, nil
}

func (c *Client) FindChangeRequest(ctx context.Context, lookup domain.ChangeRequestLookup) (domain.ChangeRequestResult, error) {
	provider, repository, remoteHost, base, reason := resolveTarget(lookup.PublicationSnapshot)
	if reason != "" {
		return domain.ChangeRequestResult{Status: domain.CapabilityUnknown, ReasonCode: reason}, runtimeError("change_request_provider_unavailable", false, nil)
	}
	if lookup.RepositoryID != repository || !validBranch(lookup.SourceBranch) || !validBranch(lookup.TargetBranch) {
		return domain.ChangeRequestResult{}, runtimeError("change_request_lookup_invalid", false, nil)
	}
	query := url.Values{"state": {"all"}, "per_page": {"100"}}
	switch provider {
	case "github":
		query.Set("head", repositoryOwner(repository)+":"+lookup.SourceBranch)
		query.Set("base", lookup.TargetBranch)
	case "gitlab":
		query.Set("source_branch", lookup.SourceBranch)
		query.Set("target_branch", lookup.TargetBranch)
	case "gitee":
		query.Set("head", repositoryOwner(repository)+":"+lookup.SourceBranch)
		query.Set("base", lookup.TargetBranch)
	}
	endpoint := repositoryEndpoint(base, repository, provider, "pulls")
	if provider == "gitlab" {
		endpoint = repositoryEndpoint(base, repository, provider, "merge_requests")
	}
	endpoint.RawQuery = query.Encode()
	payload, err := c.requestJSON(ctx, lookup.ProjectID, lookup.PublicationSnapshot, provider, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return domain.ChangeRequestResult{}, err
	}
	return findExisting(provider, repository, remoteHost, lookup, payload)
}

func (c *Client) CreateChangeRequest(ctx context.Context, request domain.ChangeRequestRequest) (domain.ChangeRequestResult, error) {
	provider, repository, remoteHost, base, reason := resolveTarget(request.PublicationSnapshot)
	if reason != "" {
		return domain.ChangeRequestResult{Status: domain.CapabilityUnknown, ReasonCode: reason}, runtimeError("change_request_provider_unavailable", false, nil)
	}
	if request.RepositoryID != repository || !validBranch(request.SourceBranch) || !validBranch(request.TargetBranch) ||
		strings.TrimSpace(request.Title) == "" || len(request.Title) > 255 || len(request.Body) > 20000 || strings.TrimSpace(request.IdempotencyKey) == "" {
		return domain.ChangeRequestResult{}, runtimeError("change_request_request_invalid", false, nil)
	}
	body := make(map[string]any)
	if provider == "github" {
		body["title"] = request.Title
		body["body"] = request.Body
		body["head"] = repositoryOwner(repository) + ":" + request.SourceBranch
		body["base"] = request.TargetBranch
		body["draft"] = request.Draft
	} else if provider == "gitlab" {
		body["title"] = request.Title
		body["description"] = request.Body
		body["source_branch"] = request.SourceBranch
		body["target_branch"] = request.TargetBranch
		body["draft"] = request.Draft
		body["remove_source_branch"] = false
		body["squash"] = false
	} else {
		body["title"] = request.Title
		body["body"] = request.Body
		body["head"] = repositoryOwner(repository) + ":" + request.SourceBranch
		body["base"] = request.TargetBranch
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return domain.ChangeRequestResult{}, runtimeError("change_request_request_invalid", false, err)
	}
	action := "pulls"
	if provider == "gitlab" {
		action = "merge_requests"
	}
	endpoint := repositoryEndpoint(base, repository, provider, action)
	payload, err := c.requestJSON(ctx, request.ProjectID, request.PublicationSnapshot, provider, http.MethodPost, endpoint, encoded, request.IdempotencyKey)
	if err != nil {
		return domain.ChangeRequestResult{}, err
	}
	return createdResult(provider, repository, remoteHost, request, payload)
}

func (c *Client) requestJSON(ctx context.Context, projectID string, snapshot domain.PublicationSnapshot, provider, method string, endpoint *url.URL, body []byte, idempotencyKey string) ([]byte, error) {
	if c == nil || c.httpClient == nil || c.credentials == nil {
		return nil, runtimeError("change_request_client_unavailable", true, nil)
	}
	if snapshot.APICredentialSecretID == "" || snapshot.APICredentialVersion < 1 {
		return nil, runtimeError("api_credential_missing", false, nil)
	}
	if strings.TrimSpace(projectID) == "" {
		return nil, runtimeError("change_request_project_invalid", false, nil)
	}
	credential, err := c.credentials.ResolveAPICredential(ctx, projectID, snapshot.APICredentialSecretID, snapshot.APICredentialVersion)
	if err != nil {
		return nil, runtimeError("api_credential_unavailable", false, err)
	}
	defer zeroBytes(credential)
	if len(credential) == 0 || len(credential) > maxCredentialBytes {
		return nil, runtimeError("api_credential_invalid", false, nil)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, runtimeError("change_request_endpoint_invalid", false, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Mendry-AutoHotfix/1.0")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	switch provider {
	case "github":
		request.Header.Set("Authorization", "Bearer "+string(credential))
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	case "gitlab":
		request.Header.Set("PRIVATE-TOKEN", string(credential))
	case "gitee":
		request.Header.Set("Authorization", "token "+string(credential))
	default:
		return nil, runtimeError("change_request_provider_unavailable", false, nil)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, runtimeError("change_request_api_unavailable", true, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, runtimeError("change_request_response_unavailable", true, err)
	}
	if len(payload) > maxResponseBytes {
		return nil, runtimeError("change_request_response_too_large", false, nil)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return nil, runtimeError("change_request_api_rejected", retryable, fmt.Errorf("provider HTTP status %d", response.StatusCode))
	}
	return payload, nil
}

func resolveTarget(snapshot domain.PublicationSnapshot) (string, string, string, *url.URL, string) {
	provider := strings.ToLower(strings.TrimSpace(snapshot.SCMProvider))
	if provider == "generic" || provider == "yunxiao" {
		return provider, "", "", nil, "provider_not_supported"
	}
	if provider != "github" && provider != "gitlab" && provider != "gitee" {
		return provider, "", "", nil, "provider_not_supported"
	}
	host, repository, scheme, err := parseRemote(snapshot.RemoteURL)
	if err != nil || repository == "" {
		return provider, "", "", nil, "repository_identity_invalid"
	}
	var base *url.URL
	if snapshot.APIBaseURL != "" {
		base, err = parseAPIBase(snapshot.APIBaseURL)
	} else {
		base, err = defaultAPIBase(provider, scheme, host)
	}
	if err != nil {
		return provider, repository, host, nil, "provider_api_base_invalid"
	}
	return provider, repository, host, base, ""
}

func parseRemote(value string) (string, string, string, error) {
	value = strings.TrimSpace(value)
	var host, repository, scheme string
	if strings.HasPrefix(value, "git@") && !strings.Contains(value, "://") {
		parts := strings.SplitN(strings.TrimPrefix(value, "git@"), ":", 2)
		if len(parts) != 2 {
			return "", "", "", fmt.Errorf("invalid SSH repository URL")
		}
		host, repository, scheme = strings.ToLower(parts[0]), parts[1], "https"
	} else {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "ssh") {
			return "", "", "", fmt.Errorf("invalid repository URL")
		}
		host, repository, scheme = strings.ToLower(parsed.Hostname()), parsed.Path, parsed.Scheme
	}
	repository = strings.Trim(strings.TrimSuffix(strings.TrimSpace(repository), ".git"), "/")
	if repository == "" || len(repository) > 1024 {
		return "", "", "", fmt.Errorf("repository path is invalid")
	}
	for _, component := range strings.Split(repository, "/") {
		if component == "" || component == "." || component == ".." || strings.ContainsAny(component, "\x00\\") {
			return "", "", "", fmt.Errorf("repository path is invalid")
		}
	}
	return host, repository, scheme, nil
}

func defaultAPIBase(provider, remoteScheme, host string) (*url.URL, error) {
	apiHost := host
	apiPath := ""
	switch provider {
	case "github":
		if host == "github.com" {
			apiHost = "api.github.com"
		} else {
			apiPath = "/api/v3"
		}
	case "gitlab":
		apiPath = "/api/v4"
	case "gitee":
		apiPath = "/api/v5"
	default:
		return nil, fmt.Errorf("unknown provider")
	}
	scheme := "https"
	if remoteScheme == "ssh" {
		scheme = "https"
	}
	return parseAPIBase(scheme + "://" + apiHost + apiPath)
}

func parseAPIBase(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimRight(value, "/"))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()))) {
		return nil, fmt.Errorf("API base must use HTTPS")
	}
	return parsed, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func repositoryEndpoint(base *url.URL, repository, provider, action string) *url.URL {
	endpoint := *base
	endpoint.Path = strings.TrimSuffix(base.Path, "/")
	endpoint.RawPath = strings.TrimSuffix(base.EscapedPath(), "/")
	if provider == "gitlab" {
		appendPathSegment(&endpoint, "projects")
		appendPathSegment(&endpoint, repository)
		if action != "" {
			appendPathSegment(&endpoint, action)
		}
		return &endpoint
	}
	appendPathSegment(&endpoint, "repos")
	for _, component := range strings.Split(repository, "/") {
		appendPathSegment(&endpoint, component)
	}
	if action != "" {
		appendPathSegment(&endpoint, action)
	}
	return &endpoint
}

func appendPathSegment(endpoint *url.URL, segment string) {
	endpoint.Path += "/" + segment
	endpoint.RawPath += "/" + url.PathEscape(segment)
}

func findExisting(provider, repository, remoteHost string, lookup domain.ChangeRequestLookup, payload []byte) (domain.ChangeRequestResult, error) {
	switch provider {
	case "github":
		var items []struct {
			Number  int    `json:"number"`
			HTMLURL string `json:"html_url"`
			Draft   bool   `json:"draft"`
			Head    struct {
				Ref  string `json:"ref"`
				Repo *struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
		}
		if err := json.Unmarshal(payload, &items); err != nil {
			return domain.ChangeRequestResult{}, runtimeError("change_request_response_invalid", false, err)
		}
		for _, item := range items {
			if item.Head.Ref == lookup.SourceBranch && item.Base.Ref == lookup.TargetBranch && item.Head.Repo != nil && item.Head.Repo.FullName == repository {
				return changeRequestResult(item.Number, item.HTMLURL, item.Draft, true, remoteHost)
			}
		}
	case "gitlab":
		var items []struct {
			IID          int    `json:"iid"`
			WebURL       string `json:"web_url"`
			Draft        bool   `json:"draft"`
			SourceBranch string `json:"source_branch"`
			TargetBranch string `json:"target_branch"`
		}
		if err := json.Unmarshal(payload, &items); err != nil {
			return domain.ChangeRequestResult{}, runtimeError("change_request_response_invalid", false, err)
		}
		for _, item := range items {
			if item.SourceBranch == lookup.SourceBranch && item.TargetBranch == lookup.TargetBranch {
				return changeRequestResult(item.IID, item.WebURL, item.Draft, true, remoteHost)
			}
		}
	case "gitee":
		var items []struct {
			Number  int    `json:"number"`
			HTMLURL string `json:"html_url"`
			State   string `json:"state"`
			Head    struct {
				Ref   string `json:"ref"`
				Label string `json:"label"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
		}
		if err := json.Unmarshal(payload, &items); err != nil {
			return domain.ChangeRequestResult{}, runtimeError("change_request_response_invalid", false, err)
		}
		for _, item := range items {
			headRef := item.Head.Ref
			if headRef == "" {
				headRef = strings.TrimPrefix(item.Head.Label, repositoryOwner(repository)+":")
			}
			if headRef == lookup.SourceBranch && item.Base.Ref == lookup.TargetBranch {
				return changeRequestResult(item.Number, item.HTMLURL, false, true, remoteHost)
			}
		}
	}
	return domain.ChangeRequestResult{Status: domain.CapabilitySupported}, nil
}

func createdResult(provider, repository, remoteHost string, request domain.ChangeRequestRequest, payload []byte) (domain.ChangeRequestResult, error) {
	switch provider {
	case "github":
		var item struct {
			Number  int    `json:"number"`
			HTMLURL string `json:"html_url"`
			Draft   bool   `json:"draft"`
			Head    struct {
				Ref  string `json:"ref"`
				Repo *struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
		}
		if err := json.Unmarshal(payload, &item); err != nil || item.Head.Ref != request.SourceBranch || item.Base.Ref != request.TargetBranch || item.Head.Repo == nil || item.Head.Repo.FullName != repository || item.Draft != request.Draft {
			return domain.ChangeRequestResult{}, runtimeError("change_request_response_invalid", false, err)
		}
		return changeRequestResult(item.Number, item.HTMLURL, item.Draft, false, remoteHost)
	case "gitlab":
		var item struct {
			IID          int    `json:"iid"`
			WebURL       string `json:"web_url"`
			Draft        bool   `json:"draft"`
			SourceBranch string `json:"source_branch"`
			TargetBranch string `json:"target_branch"`
		}
		if err := json.Unmarshal(payload, &item); err != nil || item.SourceBranch != request.SourceBranch || item.TargetBranch != request.TargetBranch || item.Draft != request.Draft {
			return domain.ChangeRequestResult{}, runtimeError("change_request_response_invalid", false, err)
		}
		return changeRequestResult(item.IID, item.WebURL, item.Draft, false, remoteHost)
	case "gitee":
		var item struct {
			Number  int    `json:"number"`
			HTMLURL string `json:"html_url"`
			Head    struct {
				Ref   string `json:"ref"`
				Label string `json:"label"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			return domain.ChangeRequestResult{}, runtimeError("change_request_response_invalid", false, err)
		}
		headRef := item.Head.Ref
		if headRef == "" {
			headRef = strings.TrimPrefix(item.Head.Label, repositoryOwner(repository)+":")
		}
		if headRef != request.SourceBranch || item.Base.Ref != request.TargetBranch {
			return domain.ChangeRequestResult{}, runtimeError("change_request_response_invalid", false, nil)
		}
		return changeRequestResult(item.Number, item.HTMLURL, false, false, remoteHost)
	}
	return domain.ChangeRequestResult{}, runtimeError("change_request_provider_unavailable", false, nil)
}

func changeRequestResult(number int, rawURL string, draft, existing bool, remoteHost string) (domain.ChangeRequestResult, error) {
	if number < 1 {
		return domain.ChangeRequestResult{}, runtimeError("change_request_response_invalid", false, nil)
	}
	return changeRequestResultWithReference("#"+strconv.Itoa(number), rawURL, draft, existing, remoteHost)
}

func changeRequestResultWithReference(reference, rawURL string, draft, existing bool, remoteHost string) (domain.ChangeRequestResult, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ToLower(parsed.Hostname()) != remoteHost ||
		(parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()))) {
		return domain.ChangeRequestResult{}, runtimeError("change_request_url_invalid", false, err)
	}
	return domain.ChangeRequestResult{
		Status: domain.CapabilitySupported, Reference: reference, URL: parsed.String(),
		Draft: draft, AlreadyExists: existing,
	}, nil
}

func repositoryOwner(repository string) string {
	parts := strings.Split(repository, "/")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], "/")
}

func validBranch(branch string) bool {
	return strings.TrimSpace(branch) == branch && branch != "" && len(branch) <= 255 &&
		!strings.ContainsAny(branch, "\x00\\~^:?*[\r\n") && !strings.Contains(branch, "..") && !strings.HasPrefix(branch, "/") && !strings.HasSuffix(branch, "/")
}

func capabilityFailureCode(err error) string {
	if runtime, ok := err.(*domain.LifecycleRuntimeError); ok {
		switch runtime.Code {
		case "api_credential_missing", "api_credential_unavailable":
			return runtime.Code
		case "change_request_api_rejected":
			return "provider_auth_or_repository_unknown"
		}
	}
	return "provider_capability_unknown"
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func runtimeError(code string, retryable bool, cause error) error {
	return &domain.LifecycleRuntimeError{Code: code, Retryable: retryable, Cause: cause}
}
