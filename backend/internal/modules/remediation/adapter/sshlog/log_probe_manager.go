package sshlog

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	projectapplication "mendry/backend/internal/modules/projects/application"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/platform/observability"
)

const (
	logProbeServiceName = "mendry-log-probe.service"
	logProbeVersion     = "1"
	logProbeMaxOutput   = 64 << 10
)

var linuxUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
var probePythonPathPattern = regexp.MustCompile(`^/[a-zA-Z0-9/_.+-]+$`)

type LogProbeManagerOptions struct {
	Secrets        SecretLoader
	Cipher         projectapplication.Cipher
	SSHCommand     string
	CommandTimeout time.Duration
	Logger         *slog.Logger
}

type LogProbeManager struct {
	secrets SecretLoader
	cipher  projectapplication.Cipher
	command string
	timeout time.Duration
	logger  *slog.Logger
}

func NewLogProbeManager(options LogProbeManagerOptions) (*LogProbeManager, error) {
	if options.Secrets == nil || options.Cipher == nil {
		return nil, fmt.Errorf("log probe manager dependencies are required")
	}
	command := strings.TrimSpace(options.SSHCommand)
	if command == "" {
		command = defaultSSHCommand
	}
	timeout := options.CommandTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	return &LogProbeManager{secrets: options.Secrets, cipher: options.Cipher, command: command, timeout: timeout, logger: options.Logger}, nil
}

var _ projectapplication.LogProbeManagerPort = (*LogProbeManager)(nil)

func (m *LogProbeManager) Install(ctx context.Context, request projectapplication.LogProbeRequest) (projectapplication.LogProbeStatus, error) {
	if err := validateLogProbeRequest(request); err != nil {
		return projectapplication.LogProbeStatus{}, err
	}
	python, err := m.preflight(ctx, request)
	if err != nil {
		return projectapplication.LogProbeStatus{}, err
	}
	config, err := json.Marshal(map[string]any{
		"schemaVersion": 1, "version": logProbeVersion, "configVersion": request.TriggerVersion,
		"logPath": request.Source.LogPath, "inboundUrl": request.InboundURL, "rules": request.Rules.Rules,
	})
	if err != nil {
		return projectapplication.LogProbeStatus{}, fmt.Errorf("encode log probe config: %w", err)
	}
	unit := "[Unit]\nDescription=Mendry managed log probe\nAfter=network-online.target\nWants=network-online.target\n\n" +
		"[Service]\nType=simple\nUser=" + request.Source.User + "\nExecStart=" + python + " /opt/mendry/log-probe.py\nRestart=always\nRestartSec=5\nNoNewPrivileges=true\nPrivateTmp=true\nProtectSystem=strict\nReadWritePaths=/var/lib/mendry-log-probe\nReadOnlyPaths=" + request.Source.LogPath + " /etc/mendry-log-probe.json /opt/mendry/log-probe.py\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
	bootstrap := renderProbeBootstrap(config, []byte(logProbePython), []byte(unit), request.Source.User, python)
	started := time.Now().UTC().Truncate(time.Second)
	if _, err := m.run(ctx, request, "log_probe_install", "sudo -n "+python+" -", []byte(bootstrap)); err != nil {
		return projectapplication.LogProbeStatus{}, err
	}
	return projectapplication.LogProbeStatus{State: "starting", Version: logProbeVersion, ConfigVersion: request.TriggerVersion, CheckedAt: started}, nil
}

func (m *LogProbeManager) preflight(ctx context.Context, request projectapplication.LogProbeRequest) (string, error) {
	script := `fail() { printf 'MENDRY_PROBE_PREFLIGHT=%s\n' "$1" >&2; exit 1; }
test -d /run/systemd/system && command -v systemctl >/dev/null 2>&1 || fail systemd_unavailable
id -u ` + strconv.Quote(request.Source.User) + ` >/dev/null 2>&1 || fail probe_user_unavailable
python=$(command -v python3) || fail python_unavailable
"$python" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 6) else 1)' || fail python_unsupported
printf 'MENDRY_PROBE_PYTHON=%s\n' "$python"
`
	output, err := m.run(ctx, request, "log_probe_preflight", "sudo -n sh -s", []byte(script))
	if err != nil {
		return "", err
	}
	python := strings.TrimPrefix(strings.TrimSpace(output), "MENDRY_PROBE_PYTHON=")
	if !strings.HasPrefix(output, "MENDRY_PROBE_PYTHON=") || !probePythonPathPattern.MatchString(python) {
		return "", &projectapplication.LogProbeSetupError{Reason: "python_unavailable"}
	}
	return python, nil
}

func (m *LogProbeManager) Status(ctx context.Context, request projectapplication.LogProbeRequest) (projectapplication.LogProbeStatus, error) {
	if err := validateLogProbeTarget(request); err != nil {
		return projectapplication.LogProbeStatus{}, err
	}
	output, err := m.run(ctx, request, "log_probe_status", "sudo -n sh -c 'systemctl is-active "+logProbeServiceName+" 2>/dev/null || true; cat /var/lib/mendry-log-probe/status.json 2>/dev/null || true'", nil)
	if err != nil {
		return projectapplication.LogProbeStatus{}, err
	}
	lines := strings.SplitN(strings.TrimSpace(output), "\n", 2)
	state := "not_installed"
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" && strings.TrimSpace(lines[0]) != "unknown" {
		state = strings.TrimSpace(lines[0])
	}
	status := projectapplication.LogProbeStatus{State: state, Version: logProbeVersion, ConfigVersion: request.TriggerVersion, CheckedAt: time.Now().UTC()}
	if len(lines) == 2 && strings.TrimSpace(lines[1]) != "" {
		var remote struct {
			Version       string `json:"version"`
			ConfigVersion int64  `json:"configVersion"`
			CheckedAt     string `json:"checkedAt"`
			Message       string `json:"message"`
		}
		if json.Unmarshal([]byte(lines[1]), &remote) == nil {
			status.Version, status.ConfigVersion, status.Message = remote.Version, remote.ConfigVersion, remote.Message
			if checked, parseErr := time.Parse(time.RFC3339, remote.CheckedAt); parseErr == nil {
				status.CheckedAt = checked
			} else {
				status.CheckedAt = time.Time{}
			}
		}
	}
	return status, nil
}

func (m *LogProbeManager) Uninstall(ctx context.Context, request projectapplication.LogProbeRequest) (projectapplication.LogProbeStatus, error) {
	if err := validateLogProbeTarget(request); err != nil {
		return projectapplication.LogProbeStatus{}, err
	}
	command := "sudo -n sh -c 'systemctl disable --now " + logProbeServiceName + " >/dev/null 2>&1 || true; rm -rf /var/lib/mendry-log-probe; rm -f /etc/systemd/system/" + logProbeServiceName + " /etc/mendry-log-probe.json /opt/mendry/log-probe.py; systemctl daemon-reload'"
	if _, err := m.run(ctx, request, "log_probe_uninstall", command, nil); err != nil {
		return projectapplication.LogProbeStatus{}, err
	}
	return projectapplication.LogProbeStatus{State: "not_installed", Version: logProbeVersion, ConfigVersion: request.TriggerVersion, CheckedAt: time.Now().UTC()}, nil
}

func validateLogProbeTarget(request projectapplication.LogProbeRequest) error {
	if request.ProjectID == "" || request.CredentialSecretID == "" ||
		request.Source.Deployment.Kind != projectdomain.SSHDeploymentHost ||
		!linuxUserPattern.MatchString(request.Source.User) {
		return fmt.Errorf("managed log probe SSH target is invalid")
	}
	return nil
}

func validateLogProbeRequest(request projectapplication.LogProbeRequest) error {
	if err := validateLogProbeTarget(request); err != nil {
		return err
	}
	if request.TriggerVersion < 1 || request.Source.Mode != "tail" ||
		!strings.HasPrefix(request.Source.LogPath, "/") ||
		(!strings.HasPrefix(request.InboundURL, "https://") && !strings.HasPrefix(request.InboundURL, "http://127.0.0.1:")) ||
		len(request.Rules.Rules) == 0 {
		return fmt.Errorf("managed log probe configuration is invalid")
	}
	if strings.ContainsAny(request.Source.LogPath, " \t\r\n") {
		return fmt.Errorf("managed log probe path is invalid")
	}
	return nil
}

func (m *LogProbeManager) run(ctx context.Context, request projectapplication.LogProbeRequest, operation, remoteCommand string, stdin []byte) (string, error) {
	encrypted, err := m.secrets.GetEncryptedSecret(ctx, request.ProjectID, request.CredentialSecretID)
	if err != nil {
		return "", fmt.Errorf("load SSH credential: %w", err)
	}
	if encrypted.Kind != projectdomain.SecretSSHPrivateKey {
		return "", fmt.Errorf("managed log probe requires an SSH private key")
	}
	plaintext, err := m.cipher.Decrypt(request.ProjectID, encrypted.ID, encrypted.Kind, encrypted.Ciphertext, encrypted.Nonce)
	if err != nil {
		return "", fmt.Errorf("decrypt SSH credential: %w", err)
	}
	defer clearBytes(plaintext)
	reader := &Reader{command: m.command, timeout: m.timeout}
	args, cleanup, err := reader.sshArgs(SourceConfig{
		ProjectID: request.ProjectID, Host: request.Source.Host, Port: request.Source.Port, User: request.Source.User,
		CredentialSecretID: request.CredentialSecretID, CredentialKind: encrypted.Kind, plaintext: plaintext,
	}, remoteCommand)
	if err != nil {
		return "", err
	}
	defer cleanup()
	runCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, m.command, args...)
	command.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	started := time.Now()
	runErr := command.Run()
	switch {
	case runErr != nil:
		runErr = probeCommandError(runCtx, runErr, stderr.String())
	case stdout.Len()+stderr.Len() > logProbeMaxOutput:
		runErr = fmt.Errorf("managed log probe SSH output exceeded limit")
	}
	m.logCommand(ctx, request, operation, remoteCommand, started, stdout.Len(), encrypted.Kind, runErr)
	if runErr != nil {
		return "", runErr
	}
	return stdout.String(), nil
}

// probeCommandError 保留远端退出状态与 stderr：这是唯一能区分 SSH 不可达、
// sudo 被拒和 bootstrap 自身失败的证据。HTTP 边界仍返回稳定的 502，诊断只进入
// 有界的内部错误。
func probeCommandError(ctx context.Context, runErr error, stderrText string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("managed log probe SSH command timed out")
	}
	diagnostic := fmt.Sprintf("managed log probe SSH command failed: %v", runErr)
	if trimmed := strings.TrimSpace(stderrText); trimmed != "" {
		diagnostic += "; stderr=" + trimmed
	}
	if bounded, _ := observability.SnapshotDiagnostic(diagnostic); bounded != "" {
		cause := errors.New(bounded)
		for _, line := range strings.Split(stderrText, "\n") {
			const preflightPrefix = "MENDRY_PROBE_PREFLIGHT="
			if strings.HasPrefix(line, preflightPrefix) {
				reason := strings.TrimSpace(strings.TrimPrefix(line, preflightPrefix))
				switch reason {
				case "python_unavailable", "python_unsupported", "systemd_unavailable", "probe_user_unavailable":
					return &projectapplication.LogProbeSetupError{Reason: reason, Cause: cause}
				}
			}
			const prefix = "MENDRY_LOG_PROBE_ERROR="
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			var failure projectapplication.LogProbePathError
			if json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &failure) != nil {
				continue
			}
			switch failure.Reason {
			case "log_path_not_found", "log_path_not_file", "log_path_not_readable":
				failure.Cause = cause
				return &failure
			}
		}
		return cause
	}
	return fmt.Errorf("managed log probe SSH command failed")
}

// logCommand 记录一次远端 probe 命令：remoteCommand 是固定字符串，remote user 与
// 命令输出不进入日志，失败原因通过有界 error_message 记录。
func (m *LogProbeManager) logCommand(
	ctx context.Context,
	request projectapplication.LogProbeRequest,
	operation, remoteCommand string,
	started time.Time,
	bytesRead int,
	credentialKind projectdomain.SecretKind,
	err error,
) {
	logger := m.logger
	var pathError *projectapplication.LogProbePathError
	if logger != nil && errors.As(err, &pathError) {
		logger = logger.With("log_path", request.Source.LogPath, "failure_stage", "validate_log_path", "reason", pathError.Reason)
	}
	observability.LogSSHEvidenceRequest(ctx, logger, observability.SSHEvidenceRequest{
		Operation: operation, Command: remoteCommand,
		Host: request.Source.Host, Port: request.Source.Port, Duration: time.Since(started), Bytes: bytesRead,
		CredentialSecretID: request.CredentialSecretID, CredentialKind: string(credentialKind), Err: err,
	})
}

func renderProbeBootstrap(config, agent, unit []byte, user, python string) string {
	encode := func(value []byte) string { return base64.StdEncoding.EncodeToString(value) }
	return `import base64, json, os, pathlib, pwd, stat, subprocess, sys
config = json.loads(base64.b64decode("` + encode(config) + `"))
log_path = pathlib.Path(config["logPath"])
def fail(reason):
    print("MENDRY_LOG_PROBE_ERROR=" + json.dumps({"reason": reason, "path": str(log_path), "user": "` + user + `"}), file=sys.stderr)
    raise SystemExit(1)
try:
    log_stat = log_path.stat()
except FileNotFoundError:
    fail("log_path_not_found")
except PermissionError:
    fail("log_path_not_readable")
if not stat.S_ISREG(log_stat.st_mode):
    fail("log_path_not_file")
account = pwd.getpwnam("` + user + `")
# Open as the service account to check directory traversal, groups and read access.
check = subprocess.run(["sudo", "-n", "-u", "` + user + `", "` + python + `", "-c", "import sys; open(sys.argv[1], 'rb').close()", str(log_path)], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
if check.returncode != 0:
    # A sudo/runtime failure must not be mislabeled as a file-permission failure.
    if b"PermissionError:" in check.stderr:
        fail("log_path_not_readable")
    if b"FileNotFoundError:" in check.stderr:
        fail("log_path_not_found")
    sys.stderr.buffer.write(check.stderr)
    raise SystemExit(check.returncode)
files = {
  "/etc/mendry-log-probe.json": ("` + encode(config) + `", 0o600),
  "/opt/mendry/log-probe.py": ("` + encode(agent) + `", 0o755),
  "/etc/systemd/system/mendry-log-probe.service": ("` + encode(unit) + `", 0o644),
}
subprocess.run(["systemctl", "--version"], check=True, stdout=subprocess.DEVNULL)
subprocess.run(["` + python + `", "--version"], check=True, stdout=subprocess.DEVNULL)
pathlib.Path("/opt/mendry").mkdir(parents=True, exist_ok=True)
pathlib.Path("/var/lib/mendry-log-probe/spool").mkdir(parents=True, exist_ok=True)
for name, (payload, mode) in files.items():
    path = pathlib.Path(name)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_bytes(base64.b64decode(payload))
    os.chmod(temporary, mode)
    os.replace(temporary, path)
os.chown("/etc/mendry-log-probe.json", account.pw_uid, account.pw_gid)
os.chown("/var/lib/mendry-log-probe", account.pw_uid, account.pw_gid)
os.chown("/var/lib/mendry-log-probe/spool", account.pw_uid, account.pw_gid)
os.chmod("/var/lib/mendry-log-probe", 0o700)
os.chmod("/var/lib/mendry-log-probe/spool", 0o700)
subprocess.run(["systemctl", "daemon-reload"], check=True)
subprocess.run(["systemctl", "enable", "mendry-log-probe.service"], check=True)
try:
    pathlib.Path("/var/lib/mendry-log-probe/status.json").unlink()
except FileNotFoundError:
    pass
subprocess.run(["systemctl", "restart", "mendry-log-probe.service"], check=True)
`
}

const logProbePython = `#!/usr/bin/python3
import collections, hashlib, json, os, pathlib, re, socket, time, urllib.error, urllib.request
CONFIG_PATH = pathlib.Path("/etc/mendry-log-probe.json")
STATE_DIR = pathlib.Path("/var/lib/mendry-log-probe")
STATE_PATH = STATE_DIR / "state.json"
STATUS_PATH = STATE_DIR / "status.json"
SPOOL = STATE_DIR / "spool"
REJECTED = STATE_DIR / "rejected"

def atomic_json(path, value):
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(value, separators=(",", ":")))
    os.replace(temporary, path)

def status(config, message):
    atomic_json(STATUS_PATH, {"version": config["version"], "configVersion": config["configVersion"], "checkedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "message": message[:240]})

def spool_event(config, event_id, event):
    queued = sorted(SPOOL.glob("*.json"))
    overflowed = len(queued) >= 1000
    while len(queued) >= 1000:
        try:
            queued.pop(0).unlink()
        except FileNotFoundError:
            pass
    atomic_json(SPOOL / (event_id + ".json"), event)
    if overflowed: status(config, "spool limit reached; oldest event dropped")

def send_spool(config):
    REJECTED.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(REJECTED, 0o700)
    failure = None
    for path in sorted(SPOOL.glob("*.json")):
        try:
            data = path.read_bytes()
            request = urllib.request.Request(config["inboundUrl"], data=data, headers={"Content-Type": "application/json"}, method="POST")
            with urllib.request.urlopen(request, timeout=15) as response:
                if 200 <= response.status < 300: path.unlink()
        except urllib.error.HTTPError as error:
            if 400 <= error.code < 500 and error.code not in (408, 429):
                if len(list(REJECTED.glob("*.json"))) >= 1000:
                    return "rejected event quarantine full"
                os.replace(path, REJECTED / path.name)
                failure = "delivery rejected: HTTP " + str(error.code)
                continue
            return "delivery retry pending: HTTP " + str(error.code)
        except Exception as error:
            return "delivery retry pending: " + type(error).__name__
    if list(REJECTED.glob("*.json")):
        return failure or "delivery rejected: events quarantined"
    return "monitoring"

def bounded_line(line):
    return line.rstrip("\r\n").encode("utf-8")[:4096].decode("utf-8", "ignore")

def bounded_event(event):
    # Go's JSON encoder also escapes HTML characters; budget for that before sending.
    def encoded_size():
        encoded = json.dumps(event, ensure_ascii=True, separators=(",", ":"))
        for char in ("&", "<", ">"):
            encoded = encoded.replace(char, "\\u%04x" % ord(char))
        return len(encoded.encode("utf-8"))
    while len(event["samples"]) > 1 and encoded_size() > 60000:
        event["samples"].pop(0)
    return event

def matches(rule, line):
    if rule.get("excludePattern") and re.search(rule["excludePattern"], line): return False
    if rule["matchType"] == "regex": return re.search(rule["pattern"], line) is not None
    return rule["pattern"] in line

def main():
    STATE_DIR.mkdir(parents=True, exist_ok=True, mode=0o700); SPOOL.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(STATE_DIR, 0o700); os.chmod(SPOOL, 0o700)
    config = json.loads(CONFIG_PATH.read_text())
    rules = config["rules"]
    windows = {rule["id"]: collections.deque() for rule in rules}
    samples = {rule["id"]: collections.deque(maxlen=20) for rule in rules}
    cooldown = {}
    state = json.loads(STATE_PATH.read_text()) if STATE_PATH.exists() else {}
    path = config["logPath"]
    first_open = not state
    while True:
        try:
            stat = os.stat(path); inode = stat.st_ino
            same_file = state.get("inode") == inode
            offset = state.get("offset", stat.st_size if first_open else 0) if same_file else (stat.st_size if first_open else 0)
            if same_file and offset > stat.st_size: offset = 0
            first_open = False
            with open(path, "r", errors="replace") as stream:
                stream.seek(min(offset, stat.st_size))
                while True:
                    line = stream.readline()
                    if not line: break
                    line = bounded_line(line)
                    if not line.strip():
                        state = {"inode": inode, "offset": stream.tell()}; atomic_json(STATE_PATH, state)
                        continue
                    now = time.time()
                    for rule in rules:
                        if not matches(rule, line): continue
                        queue = windows[rule["id"]]; queue.append(now); samples[rule["id"]].append(line)
                        while queue and queue[0] < now - rule["windowSeconds"]: queue.popleft()
                        if len(queue) >= rule["threshold"] and cooldown.get(rule["id"], 0) <= now:
                            seed = rule["id"] + str(queue[0]) + str(now) + "\n".join(samples[rule["id"]])
                            event_id = hashlib.sha256(seed.encode()).hexdigest()
                            event = {"schemaVersion":1,"eventId":event_id,"ruleId":rule["id"],"configVersion":config["configVersion"],"matchCount":len(queue),"windowStartedAt":time.strftime("%Y-%m-%dT%H:%M:%SZ",time.gmtime(queue[0])),"triggeredAt":time.strftime("%Y-%m-%dT%H:%M:%SZ",time.gmtime(now)),"host":socket.gethostname(),"samples":list(samples[rule["id"]])}
                            spool_event(config, event_id, bounded_event(event))
                            cooldown[rule["id"]] = now + rule["cooldownSeconds"]
                            queue.clear(); samples[rule["id"]].clear()
                    state = {"inode": inode, "offset": stream.tell()}; atomic_json(STATE_PATH, state)
            status(config, send_spool(config))
        except Exception as error:
            status(config, "read failed: " + type(error).__name__)
        time.sleep(2)
if __name__ == "__main__": main()
`
