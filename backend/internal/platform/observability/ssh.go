package observability

import (
	"context"
	"log/slog"
	"time"
)

// SSHEvidenceRequest 是一次 SSH evidence 读取的完成记录。
type SSHEvidenceRequest struct {
	Operation          string
	Command            string
	Host               string
	Port               int
	Duration           time.Duration
	Bytes              int
	CredentialSecretID string
	CredentialKind     string
	Err                error
}

// LogSSHEvidenceRequest 记录 SSH evidence 读取结果；command 只表示远端执行
// 的命令，stderr/错误原因通过有界 error_message 记录。
func LogSSHEvidenceRequest(ctx context.Context, logger *slog.Logger, rec SSHEvidenceRequest) {
	if logger == nil {
		return
	}
	outcome := "success"
	level := slog.LevelDebug
	class := ClassifyOutbound(rec.Err, 0)
	if rec.Command != "" {
		class = ClassifyCommand(ctx, rec.Err)
	}
	if rec.Err != nil {
		outcome = "failure"
		level = outboundFailureLevel(class)
	}
	attrs := []slog.Attr{
		slog.String(FieldComponent, "sshlog"),
		slog.String("ssh.operation.name", rec.Operation),
		slog.String("ssh.host", rec.Host),
		slog.Int("ssh.port", rec.Port),
		slog.Int64(FieldDurationMS, rec.Duration.Milliseconds()),
		slog.Int("bytes", rec.Bytes),
		slog.String(FieldOutcome, outcome),
	}
	if rec.Command != "" {
		attrs = append(attrs, slog.String(FieldSSHCommand, rec.Command))
	}
	if rec.CredentialSecretID != "" {
		attrs = append(attrs, slog.String(FieldCredentialSecretID, rec.CredentialSecretID))
	}
	if rec.CredentialKind != "" {
		attrs = append(attrs, slog.String(FieldCredentialKind, rec.CredentialKind))
	}
	if class != "" {
		attrs = append(attrs, slog.String(FieldErrorClass, class))
	}
	if rec.Err != nil {
		if message, truncated := SnapshotDiagnostic(rec.Err.Error()); message != "" {
			attrs = append(attrs, slog.String(FieldErrorMessage, message))
			if truncated {
				attrs = append(attrs, slog.Bool(FieldErrorMessageTruncated, true))
			}
		}
	}
	Log(ctx, logger, level, EventSSHEvidenceCompleted, "SSH evidence request completed", attrs...)
}
