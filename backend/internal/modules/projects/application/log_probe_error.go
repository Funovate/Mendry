package application

import "fmt"

// LogProbeSetupError describes a known remote capability or startup failure without exposing SSH output.
type LogProbeSetupError struct {
	Reason string
	Cause  error
}

func (e *LogProbeSetupError) Message() string {
	switch e.Reason {
	case "python_unavailable":
		return "Python 3 is not available on the SSH host. Install Python 3.6 or newer."
	case "python_unsupported":
		return "The SSH host requires Python 3.6 or newer for the managed log probe."
	case "systemd_unavailable":
		return "The SSH host must run systemd to install the managed log probe."
	case "probe_user_unavailable":
		return "The configured probe user does not exist on the SSH host."
	case "service_not_ready":
		return "The log probe was installed but did not become healthy. Check the remote service status."
	default:
		return "The SSH host does not meet the managed log probe requirements."
	}
}

func (e *LogProbeSetupError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message(), e.Cause)
	}
	return e.Message()
}

func (e *LogProbeSetupError) Unwrap() error { return e.Cause }

// LogProbePathError carries only a recognized configuration failure, never raw SSH stderr.
type LogProbePathError struct {
	Reason string `json:"reason"`
	Path   string `json:"path"`
	User   string `json:"user"`
	Cause  error  `json:"-"`
}

func (e *LogProbePathError) Message() string {
	switch e.Reason {
	case "log_path_not_found":
		return fmt.Sprintf("Log file %q does not exist on the SSH host. Check the host path; container paths must be mapped to the host.", e.Path)
	case "log_path_not_file":
		return fmt.Sprintf("Log path %q on the SSH host is not a regular file. Select a specific log file, not a directory or wildcard.", e.Path)
	case "log_path_not_readable":
		return fmt.Sprintf("Log file %q cannot be read by probe user %q. Check file and parent-directory permissions.", e.Path, e.User)
	default:
		return "The remote log path could not be validated."
	}
}

func (e *LogProbePathError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message(), e.Cause)
	}
	return e.Message()
}

func (e *LogProbePathError) Unwrap() error { return e.Cause }
