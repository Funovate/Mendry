package sshlog

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func runManagedProbePython(t *testing.T, harness string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required")
	}
	cmd := exec.Command(python, "-c", harness)
	cmd.Stdin = strings.NewReader(logProbePython)
	cmd.Env = append(os.Environ(), "PROBE_TEST_DIR="+t.TempDir())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("managed probe script failed: %v\n%s", err, output)
	}
}

func TestManagedProbeBoundsSamplesAndEvents(t *testing.T) {
	runManagedProbePython(t, `import json, sys
scope = {"__name__": "probe_test"}
exec(compile(sys.stdin.read(), "probe.py", "exec"), scope)
line = scope["bounded_line"]("错误" * 1500)
assert len(line.encode("utf-8")) <= 4096
assert line and len(line.encode("utf-8")) <= 4096
event = {"samples": ["ERROR" + "&" * 4091] * 20}
scope["bounded_event"](event)
assert event["samples"] and len(event["samples"]) < 20
encoded = json.dumps(event, ensure_ascii=True).replace("&", "\\u0026")
assert len(encoded.encode("utf-8")) <= 65536
`)
}

func TestManagedProbeQuarantinesPermanentFailuresAndKeepsRetryStatus(t *testing.T) {
	runManagedProbePython(t, `import os, pathlib, sys, urllib.error
scope = {"__name__": "probe_test"}
exec(compile(sys.stdin.read(), "probe.py", "exec"), scope)
root = pathlib.Path(os.environ["PROBE_TEST_DIR"])
spool = root / "spool"
spool.mkdir()
scope["SPOOL"] = spool
scope["REJECTED"] = root / "rejected"
(spool / "a.json").write_text("bad")
(spool / "b.json").write_text("good")
class Response:
    status = 202
    def __enter__(self): return self
    def __exit__(self, *args): pass
def send(request, timeout):
    if request.data == b"bad":
        raise urllib.error.HTTPError(request.full_url, 400, "bad", {}, None)
    return Response()
scope["urllib"].request.urlopen = send
config = {"inboundUrl": "https://example.test/hooks/token"}
assert scope["send_spool"](config) == "delivery rejected: HTTP 400"
assert not list(spool.glob("*.json"))
assert len(list(scope["REJECTED"].glob("*.json"))) == 1
assert __import__("stat").S_IMODE(scope["REJECTED"].stat().st_mode) == 0o700
assert scope["send_spool"](config) == "delivery rejected: events quarantined"
(spool / "c.json").write_text("retry")
def unavailable(request, timeout):
    raise urllib.error.HTTPError(request.full_url, 503, "unavailable", {}, None)
scope["urllib"].request.urlopen = unavailable
assert scope["send_spool"](config) == "delivery retry pending: HTTP 503"
assert (spool / "c.json").exists()
`)
}

func TestManagedProbeQuarantineIsBounded(t *testing.T) {
	runManagedProbePython(t, `import os, pathlib, sys, urllib.error
scope = {"__name__": "probe_test"}
exec(compile(sys.stdin.read(), "probe.py", "exec"), scope)
root = pathlib.Path(os.environ["PROBE_TEST_DIR"])
scope["SPOOL"] = root / "spool"
scope["SPOOL"].mkdir()
scope["REJECTED"] = root / "rejected"
scope["REJECTED"].mkdir()
for index in range(1000): (scope["REJECTED"] / (str(index) + ".json")).touch()
(scope["SPOOL"] / "event.json").write_text("bad")
def reject(request, timeout):
    raise urllib.error.HTTPError(request.full_url, 400, "bad", {}, None)
scope["urllib"].request.urlopen = reject
assert scope["send_spool"]({"inboundUrl": "https://example.test/hooks/token"}) == "rejected event quarantine full"
assert (scope["SPOOL"] / "event.json").exists()
`)
}
