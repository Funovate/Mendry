// Command agentcore-local 提供 account-free 的本地 Agent Core runner、inspect 和 recovery 操作。
//
// 该命令不创建 PostgreSQL、Redis、account 或其他 service 依赖；业务行为由
// internal/modules/agentcore/command 负责，main 只处理参数、signal 和输出边界。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mendry/backend/internal/modules/agentcore/command"
	"mendry/backend/internal/modules/agentcore/domain"
)

const maxResolutionBytes = 1 << 20

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "run":
			return runCommand(args[1:])
		case "inspect":
			return inspectCommand(args[1:])
		case "resolve":
			return resolveCommand(args[1:])
		}
	}
	// 保留最初版本的 flag-only 调用形式，避免已有 onboarding 命令突然失效。
	return runCommand(args)
}

func runCommand(args []string) int {
	flags := flag.NewFlagSet("agentcore-local run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "trusted local Agent Core configuration JSON")
	eventPath := flags.String("event", "", "local Agent Core event JSON")
	stateDir := flags.String("state-dir", ".agentcore/runs", "directory for durable run snapshots")
	resume := flags.Bool("resume", false, "require and resume the existing durable run")
	if err := flags.Parse(args); err != nil {
		printUsage()
		return 2
	}
	if flags.NArg() != 0 || *configPath == "" || *eventPath == "" {
		printUsage()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	_, err := command.Run(ctx, command.Options{
		ConfigPath: *configPath,
		EventPath:  *eventPath,
		StateDir:   *stateDir,
		Resume:     *resume,
		Env:        func(key string) (string, bool) { return os.LookupEnv(key) },
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	})
	if err != nil {
		printError(err)
		return 1
	}
	return 0
}

func inspectCommand(args []string) int {
	flags := flag.NewFlagSet("agentcore-local inspect", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	stateDir := flags.String("state-dir", ".agentcore/runs", "directory containing durable run snapshots")
	runID := flags.String("run-id", "", "durable run ID")
	if err := flags.Parse(args); err != nil {
		printUsage()
		return 2
	}
	if flags.NArg() != 0 || *runID == "" {
		printUsage()
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	snapshot, err := command.Inspect(ctx, *stateDir, *runID)
	if err != nil {
		printError(err)
		return 1
	}
	if err := writeJSON(os.Stdout, snapshot); err != nil {
		printError(err)
		return 1
	}
	return 0
}

func resolveCommand(args []string) int {
	flags := flag.NewFlagSet("agentcore-local resolve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	stateDir := flags.String("state-dir", ".agentcore/runs", "directory containing durable run snapshots")
	runID := flags.String("run-id", "", "durable run ID")
	invocationID := flags.String("invocation-id", "", "pending or unknown invocation ID")
	state := flags.String("state", string(domain.InvocationSucceeded), "operator outcome: succeeded or failed")
	code := flags.String("code", "operator_resolved", "bounded operator outcome code")
	outputJSON := flags.String("output-json", "", "operator outcome JSON value")
	outputFile := flags.String("output-file", "", "file containing operator outcome JSON value")
	completedAt := flags.String("completed-at", "", "optional RFC3339Nano completion timestamp")
	if err := flags.Parse(args); err != nil {
		printUsage()
		return 2
	}
	if flags.NArg() != 0 || *runID == "" || *invocationID == "" || *outputJSON != "" && *outputFile != "" {
		printUsage()
		return 2
	}

	var output any
	var err error
	if *outputFile != "" {
		encoded, readErr := os.ReadFile(*outputFile)
		if readErr != nil {
			printError(readErr)
			return 1
		}
		if len(encoded) > maxResolutionBytes {
			printError(errors.New("operator outcome file exceeds the bound"))
			return 1
		}
		output, err = decodeJSON(encoded)
	} else if *outputJSON != "" {
		if len(*outputJSON) > maxResolutionBytes {
			printError(errors.New("operator outcome JSON exceeds the bound"))
			return 1
		}
		output, err = decodeJSON([]byte(*outputJSON))
	}
	if err != nil {
		printError(fmt.Errorf("decode operator outcome: %w", err))
		return 1
	}
	if *state != string(domain.InvocationSucceeded) && *state != string(domain.InvocationFailed) {
		printError(errors.New("operator outcome state must be succeeded or failed"))
		return 2
	}
	finished := time.Time{}
	if *completedAt != "" {
		finished, err = time.Parse(time.RFC3339Nano, *completedAt)
		if err != nil {
			printError(fmt.Errorf("parse completed-at: %w", err))
			return 2
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	snapshot, err := command.ResolveInvocation(ctx, *stateDir, *runID, *invocationID, domain.InvocationState(*state), *code, output, finished)
	if err != nil {
		printError(err)
		return 1
	}
	if err := writeJSON(os.Stdout, snapshot); err != nil {
		printError(err)
		return 1
	}
	return 0
}

func decodeJSON(encoded []byte) (any, error) {
	if len(bytes.TrimSpace(encoded)) == 0 {
		return nil, errors.New("JSON input is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("JSON input has trailing data")
		}
		return nil, err
	}
	return value, nil
}

func writeJSON(writer io.Writer, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	_, err = writer.Write(encoded)
	return err
}

func printError(err error) {
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "agentcore-local: %v\n", err)
	}
}

func printUsage() {
	_, _ = fmt.Fprintln(os.Stderr, "usage:")
	_, _ = fmt.Fprintln(os.Stderr, "  agentcore-local [run] --config <config.json> --event <event.json> [--state-dir <dir>] [--resume]")
	_, _ = fmt.Fprintln(os.Stderr, "  agentcore-local inspect --state-dir <dir> --run-id <run-id>")
	_, _ = fmt.Fprintln(os.Stderr, "  agentcore-local resolve --state-dir <dir> --run-id <run-id> --invocation-id <id> [--state succeeded|failed] [--output-json <json>|--output-file <path>]")
}
