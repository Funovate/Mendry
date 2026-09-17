package application

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxInspectCommandBytes = 4096
	maxInspectPipelineSegs = 3
)

// inspectValidator 对某个命令族在已解析 argv 上的只读语义做显式校验。校验只
// 发生在 parsed argv 上，绝不把模型原始字符串交给远端 shell；命令族若无法从
// argv 证明只读，则宁可不注册，也不能依赖 harness 超时兜底。
type inspectValidator func(argv []inspectToken) error

// inspectCommandPolicies 是 command-policy registry：简单只读命令共享
// alwaysReadInspect，混合读写命令族（hostname/date/find/journalctl/dmesg/ss/
// ip/systemctl/docker）使用显式语义校验。env/printenv 已移除：它们的输出会
// 泄露凭据与无关进程配置。
var inspectCommandPolicies = map[string]inspectValidator{
	// 纯只读文件/显示/系统检查命令族。
	"ls": alwaysReadInspect, "cat": alwaysReadInspect, "head": alwaysReadInspect,
	"grep": alwaysReadInspect, "egrep": alwaysReadInspect, "fgrep": alwaysReadInspect,
	"stat": alwaysReadInspect, "wc": alwaysReadInspect, "file": alwaysReadInspect,
	"readlink": alwaysReadInspect, "realpath": alwaysReadInspect, "pwd": alwaysReadInspect,
	"uname": alwaysReadInspect, "df": alwaysReadInspect, "du": alwaysReadInspect,
	"ps": alwaysReadInspect, "id": alwaysReadInspect, "free": alwaysReadInspect,
	"uptime": alwaysReadInspect, "lscpu": alwaysReadInspect, "lsblk": alwaysReadInspect,
	"lsof": alwaysReadInspect, "netstat": alwaysReadInspect, "getent": alwaysReadInspect,
	"who": alwaysReadInspect, "w": alwaysReadInspect, "last": alwaysReadInspect,
	// 混合读写命令族：必须逐族校验。
	"tail":       validateInspectTail,
	"hostname":   validateInspectHostname,
	"date":       validateInspectDate,
	"find":       validateInspectFind,
	"journalctl": validateInspectJournalctl,
	"dmesg":      validateInspectDmesg,
	"ss":         validateInspectSS,
	"ip":         validateInspectIP,
	"systemctl":  validateInspectSystemctl,
	"docker":     validateInspectDocker,
}

// parsedInspectCommand 保存 gateway 重建后的 quoted argv。Command 才是适配器
// 实际执行的远端字符串，不得把模型原始输入交给 SSH。
type parsedInspectCommand struct {
	Command string
}

// inspectToken 保留解码后的 argv 文本，以及 glob 元字符是否出现在未加引号、
// 未转义的位置。引号内的 *?[ 和 | 是字面量；未加引号的 ls *.log 或裸管道仍须在 SSH 前拒绝。
type inspectToken struct {
	value        string
	unquotedGlob bool
	pipe         bool
}

// parseInspectCommand 在任何 SSH 调用之前把模型字符串解析成允许的 inspect
// 命令。拒绝策略必须稳定：非法语法、重定向、环境赋值、sudo 和相对 .. 都
// 返回 invalid_arguments，而不是把原始字符串交给远端 shell。
func parseInspectCommand(raw string) (parsedInspectCommand, error) {
	if strings.TrimSpace(raw) == "" {
		return parsedInspectCommand{}, fmt.Errorf("command is required")
	}
	if !utf8.ValidString(raw) {
		return parsedInspectCommand{}, fmt.Errorf("command contains invalid utf-8")
	}
	if len(raw) > maxInspectCommandBytes {
		return parsedInspectCommand{}, fmt.Errorf("command exceeds bound")
	}
	if strings.ContainsAny(raw, "\n\r") {
		return parsedInspectCommand{}, fmt.Errorf("command contains a newline")
	}
	if strings.ContainsRune(raw, 0) {
		return parsedInspectCommand{}, fmt.Errorf("command contains a null byte")
	}
	if strings.Contains(raw, "`") || strings.Contains(raw, "$(") {
		return parsedInspectCommand{}, fmt.Errorf("command substitution is not allowed")
	}
	tokens, err := tokenizeInspect(raw)
	if err != nil {
		return parsedInspectCommand{}, err
	}
	segments, err := splitInspectPipeline(tokens)
	if err != nil {
		return parsedInspectCommand{}, err
	}
	quoted := make([]string, 0, len(segments))
	for _, segment := range segments {
		if err := validateInspectSegment(segment); err != nil {
			return parsedInspectCommand{}, err
		}
		quoted = append(quoted, quoteInspectArgv(segment))
	}
	return parsedInspectCommand{Command: strings.Join(quoted, " | ")}, nil
}

func tokenizeInspect(raw string) ([]inspectToken, error) {
	var tokens []inspectToken
	var current strings.Builder
	quote := rune(0)
	escaped := false
	unquotedGlob := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, inspectToken{value: current.String(), unquotedGlob: unquotedGlob})
		current.Reset()
		unquotedGlob = false
	}
	for _, r := range raw {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if quote == 0 && r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ';', '&', '<', '>', '(', ')', '{', '}', '\t':
			return nil, fmt.Errorf("command contains rejected syntax")
		case '|':
			flush()
			if len(tokens) > 0 && tokens[len(tokens)-1].pipe {
				return nil, fmt.Errorf("command contains rejected syntax")
			}
			tokens = append(tokens, inspectToken{value: "|", pipe: true})
		default:
			if unicode.IsSpace(r) {
				flush()
				continue
			}
			if isGlobMeta(r) {
				unquotedGlob = true
			}
			current.WriteRune(r)
		}
	}
	if escaped {
		return nil, fmt.Errorf("command ends with a dangling escape")
	}
	if quote != 0 {
		return nil, fmt.Errorf("command has an unclosed quote")
	}
	flush()
	if len(tokens) == 0 {
		return nil, fmt.Errorf("command is required")
	}
	return tokens, nil
}

func splitInspectPipeline(tokens []inspectToken) ([][]inspectToken, error) {
	var segments [][]inspectToken
	current := make([]inspectToken, 0, len(tokens))
	for _, token := range tokens {
		if token.pipe {
			if len(current) == 0 {
				return nil, fmt.Errorf("command has an empty pipeline segment")
			}
			segments = append(segments, current)
			current = nil
			continue
		}
		current = append(current, token)
	}
	if len(current) == 0 {
		return nil, fmt.Errorf("command has an empty pipeline segment")
	}
	segments = append(segments, current)
	if len(segments) > maxInspectPipelineSegs {
		return nil, fmt.Errorf("command pipeline exceeds three segments")
	}
	return segments, nil
}

// validateInspectSegment 校验一个 pipeline segment：二进制名必须注册，且整段
// argv 必须通过该命令族的只读策略与通用 env/路径/glob 检查。每个 segment 都
// 独立校验，任何一段不合格都在 SSH 前整体拒绝。
func validateInspectSegment(argv []inspectToken) error {
	if len(argv) == 0 {
		return fmt.Errorf("command has an empty pipeline segment")
	}
	binary := argv[0].value
	if strings.Contains(binary, "=") {
		return fmt.Errorf("environment assignment is not allowed")
	}
	if strings.Contains(binary, "/") {
		return fmt.Errorf("command binary must be an allowlisted inspect name")
	}
	validator, ok := inspectCommandPolicies[binary]
	if !ok {
		if binary == "sudo" {
			return fmt.Errorf("sudo is not allowed")
		}
		return fmt.Errorf("command binary %q is not allowlisted", binary)
	}
	if err := validator(argv); err != nil {
		return err
	}
	for _, arg := range argv {
		if strings.Contains(arg.value, "=") && !strings.HasPrefix(arg.value, "-") {
			return fmt.Errorf("environment assignment is not allowed")
		}
		if strings.Contains(arg.value, "..") {
			return fmt.Errorf("relative parent path segments are not allowed")
		}
		if arg.unquotedGlob {
			return fmt.Errorf("unquoted glob expansion is not allowed")
		}
	}
	return nil
}

// alwaysReadInspect 是共享的 no-mutation 校验器。这些命令族在 parsed argv 上
// 不存在可用的变异形态；通用 env/路径/glob 检查仍然生效。
func alwaysReadInspect([]inspectToken) error {
	return nil
}

// isGlobMeta 只识别未加引号时才会被远端 shell 展开的通配符。
func isGlobMeta(r rune) bool {
	return r == '*' || r == '?' || r == '['
}

func quoteInspectArgv(argv []inspectToken) string {
	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, shellQuote(arg.value))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// rejectInspectOptions 拒绝 argv 中出现任一精确匹配或前缀匹配的禁止选项。
func rejectInspectOptions(argv []inspectToken, exact []string, prefixes []string) error {
	for _, arg := range argv {
		for _, option := range exact {
			if arg.value == option {
				return fmt.Errorf("option %s is not allowed", option)
			}
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(arg.value, prefix) {
				return fmt.Errorf("option %s is not allowed", arg.value)
			}
		}
	}
	return nil
}

// checkInspectShortOptions 校验短选项 token 只包含允许字符。短选项可以组合
// （如 journalctl -fb），必须逐字符检查，否则组合 token 会绕过精确匹配。
func checkInspectShortOptions(argv []inspectToken, allowed string) error {
	for _, arg := range argv {
		value := arg.value
		if len(value) < 2 || value[0] != '-' || value[1] == '-' {
			continue
		}
		for index := 1; index < len(value); index++ {
			if !strings.ContainsRune(allowed, rune(value[index])) {
				return fmt.Errorf("option %s is not allowed", value)
			}
		}
	}
	return nil
}

// checkInspectLongOptions 拒绝不在允许列表中的长选项；-- 是 end-of-options
// 标记，--name=value 按 = 前的选项名匹配。
func checkInspectLongOptions(argv []inspectToken, allowed []string) error {
	for _, arg := range argv {
		value := arg.value
		if !strings.HasPrefix(value, "--") {
			continue
		}
		if value == "--" {
			continue
		}
		name := value
		if index := strings.IndexByte(name, '='); index >= 0 {
			name = name[:index]
		}
		if !containsInspectString(allowed, name) {
			return fmt.Errorf("option %s is not allowed", value)
		}
	}
	return nil
}

// containsInspectOption 判断 argv 中是否出现给定选项（含 --name=value 形式）。
func containsInspectOption(argv []inspectToken, option string) bool {
	for _, arg := range argv {
		if arg.value == option || strings.HasPrefix(arg.value, option+"=") {
			return true
		}
	}
	return false
}

func containsInspectString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// validateInspectTail 拒绝 follow/retry 流式形态：tail -f / -F / --follow /
// --retry 会无限阻塞远端，只能依赖 harness 超时终止。--follow 带可选参数
// （--follow=name），必须按前缀拒绝，否则精确匹配会放过流式赋值形态。
func validateInspectTail(argv []inspectToken) error {
	if err := rejectInspectOptions(argv[1:], []string{"-f", "-F", "--follow", "--retry"}, []string{"--follow"}); err != nil {
		return err
	}
	return checkInspectShortOptions(argv[1:], "cnqsvz")
}

// validateInspectHostname 只允许读取形态：hostname -I / -i / -f / -s / -d 等。
// 位置参数会设置主机名（变异）；-F/--file 与 -b/--boot 也会写入主机名。
// --file=NAME 必须按前缀拒绝：hostname 无长选项白名单，精确匹配 --file 会放过
// getopt_long 支持的 --file= 赋值形态。
func validateInspectHostname(argv []inspectToken) error {
	for _, arg := range argv[1:] {
		if !strings.HasPrefix(arg.value, "-") {
			return fmt.Errorf("hostname positional arguments are not allowed")
		}
	}
	if err := rejectInspectOptions(argv[1:], []string{"-F", "--file", "-b", "--boot"}, []string{"--file"}); err != nil {
		return err
	}
	return checkInspectShortOptions(argv[1:], "aAdfiIsyhV")
}

// validateInspectDate 只允许显示/解析格式。-s/--set 会设置系统时钟，-f/--file
// 会从文件逐行读取日期：在旧版 coreutils（RHEL/CentOS/TencentOS 常见）上
// -f FILE 会按行 SET 系统时钟，属于 design 的 file timestamp mutation 拒绝项，
// 因此连同 --file= 赋值形态一并拒绝。
func validateInspectDate(argv []inspectToken) error {
	if err := rejectInspectOptions(argv[1:], []string{"-s", "--set", "-f", "--file"}, nil); err != nil {
		return err
	}
	if err := checkInspectShortOptions(argv[1:], "dIRruhV"); err != nil {
		return err
	}
	return checkInspectLongOptions(argv[1:], []string{
		"--date", "--debug", "--help", "--iso-8601", "--reference",
		"--rfc-2822", "--rfc-3339", "--rfc-email", "--universal", "--utc", "--version",
	})
}

// validateInspectFind 允许谓词与打印到 stdout 的动作；任何写文件或执行命令的
// 动作（-delete / -exec* / -ok* / -fprint* / -fls）都在 SSH 前拒绝。
func validateInspectFind(argv []inspectToken) error {
	return rejectInspectOptions(argv[1:], []string{"-delete", "-fls"}, []string{"-exec", "-ok", "-fprint"})
}

var journalctlReadLongOptions = []string{
	"--after-cursor", "--all", "--boot", "--catalog", "--case-sensitive", "--cursor",
	"--directory", "--disk-usage", "--dmesg", "--dump-catalog", "--facility", "--field",
	"--fields", "--file", "--grep", "--header", "--help", "--identifier", "--lines",
	"--list-boots", "--list-catalog", "--merge", "--namespace", "--no-full",
	"--no-hostname", "--no-pager", "--no-tail", "--output", "--output-fields",
	"--pager-end", "--priority", "--quiet", "--reverse", "--show-cursor", "--since",
	"--system", "--time-format", "--unit", "--until", "--user", "--user-unit", "--utc",
	"--verify", "--version",
}

// validateInspectJournalctl 只允许有界历史/状态读取。follow、vacuum、rotate、
// flush、sync、key/catalog 变更和 relinquish 等写操作在 SSH 前拒绝；
// -M/--machine/--root 会把读取切换到另一台主机或目录，同样拒绝。
func validateInspectJournalctl(argv []inspectToken) error {
	if err := rejectInspectOptions(argv[1:],
		[]string{"-f", "--follow", "--force", "--flush", "--machine", "--new-id128",
			"--relinquish-var", "--root", "--rotate", "--setup-keys",
			"--smart-relinquish-var", "--sync", "--update-catalog", "--verify-key",
			"-M", "--add-catalog"},
		[]string{"--vacuum-"}); err != nil {
		return err
	}
	if err := checkInspectShortOptions(argv[1:], "abDeghkmnopqrStUuxNF"); err != nil {
		return err
	}
	return checkInspectLongOptions(argv[1:], journalctlReadLongOptions)
}

var dmesgReadLongOptions = []string{
	"--buffer-size", "--color", "--ctime", "--decode", "--facility", "--help",
	"--human", "--kernel", "--level", "--noescape", "--nopager", "--notime",
	"--raw", "--reltime", "--show-delta", "--since", "--syslog", "--time-format",
	"--until", "--userspace", "--version",
}

// validateInspectDmesg 只允许显示/过滤读取。clear/read-clear 与 console
// on/off/level 变更在 SSH 前拒绝；-w/--follow 是流式读取，同样拒绝。
func validateInspectDmesg(argv []inspectToken) error {
	if err := rejectInspectOptions(argv[1:],
		[]string{"-c", "--clear", "-C", "--read-clear", "-n", "--console-level",
			"--console-on", "--console-off", "-w", "--follow"},
		nil); err != nil {
		return err
	}
	if err := checkInspectShortOptions(argv[1:], "deHkLPrsSTtuxfl"); err != nil {
		return err
	}
	return checkInspectLongOptions(argv[1:], dmesgReadLongOptions)
}

// validateInspectSS 只允许 socket 列举/过滤；-K/--kill 会终止 socket，
// -D/--diag 会把 socket 信息写入文件（变异）。--diag=FILE 必须按前缀拒绝：
// ss 无长选项白名单，精确匹配 --diag 会放过 getopt_long 支持的 --diag= 赋值形态。
func validateInspectSS(argv []inspectToken) error {
	if err := rejectInspectOptions(argv[1:], []string{"-K", "--kill", "-D", "--diag"}, []string{"--diag"}); err != nil {
		return err
	}
	return checkInspectShortOptions(argv[1:], "aAdefFiIlmnoprstuxZ")
}

// ipReadActions 记录每个只读 ip 子命令允许的动作。任何 add/del/set/change/
// delete/flush/replace/exec/monitor 动作都不在表中，自然被拒绝。
var ipReadActions = map[string][]string{
	"address": {"show", "list", "get"}, "addr": {"show", "list", "get"}, "a": {"show", "list", "get"},
	"link": {"show", "list"}, "l": {"show", "list"},
	"route": {"show", "list", "get"}, "r": {"show", "list", "get"},
	"rule": {"show", "list"}, "ru": {"show", "list"},
	"neigh": {"show", "list", "get"}, "neighbour": {"show", "list", "get"}, "n": {"show", "list", "get"},
	"maddr":  {"show", "list"},
	"tunnel": {"show", "list"}, "tunl": {"show", "list"},
	"netns": {"list"},
}

// validateInspectIP 只允许 ip 的 show/list/get 只读子命令与 netns list。
// 全局选项只接受不携带参数、不影响目标的读取选项；-n/--netns（切换 netns）、
// -b/--batch（从文件读命令）与 -force 直接拒绝。
func validateInspectIP(argv []inspectToken) error {
	args := argv[1:]
	if len(args) == 0 {
		return fmt.Errorf("ip requires a subcommand")
	}
	if err := checkInspectShortOptions(args, "46sdorcpja"); err != nil {
		return err
	}
	if err := rejectInspectOptions(args,
		[]string{"-n", "-netns", "-b", "-batch", "-force", "-h", "--help", "--version"},
		nil); err != nil {
		return err
	}
	subIndex := -1
	for index, arg := range args {
		if strings.HasPrefix(arg.value, "-") {
			continue
		}
		subIndex = index
		break
	}
	if subIndex < 0 {
		return fmt.Errorf("ip requires a subcommand")
	}
	subcommand := args[subIndex].value
	actions, ok := ipReadActions[subcommand]
	if !ok {
		return fmt.Errorf("ip subcommand %q is not allowed", subcommand)
	}
	// 子命令后的第一个非选项 token 必须是允许的只读动作。
	for index := subIndex + 1; index < len(args); index++ {
		arg := args[index]
		if strings.HasPrefix(arg.value, "-") {
			continue
		}
		if !containsInspectString(actions, arg.value) {
			return fmt.Errorf("ip action %q is not allowed", arg.value)
		}
		break
	}
	return nil
}

var systemctlReadLongOptions = []string{
	"--all", "--full", "--help", "--legend", "--lines", "--no-legend", "--no-pager",
	"--output", "--plain", "--property", "--quiet", "--reverse", "--state", "--type",
	"--version",
}

// validateInspectSystemctl 只允许 status/show/cat/list-*/is-* 只读子命令。
// start/stop/restart/reload/enable/disable/mask/edit/set-property/daemon-reload
// 等状态变更在 SSH 前拒绝；-H/-M/--root/--force/--now/--runtime 同样拒绝。
// 子命令必须是第一个参数，避免 -t/-p/-o/-n 等带参选项吞掉子命令位置。
func validateInspectSystemctl(argv []inspectToken) error {
	args := argv[1:]
	if len(args) == 0 {
		return fmt.Errorf("systemctl requires a subcommand")
	}
	if err := checkInspectShortOptions(args, "alnopqrt"); err != nil {
		return err
	}
	if err := rejectInspectOptions(args,
		[]string{"-f", "--force", "--now", "--runtime", "-H", "--host", "-M", "--machine", "--root"},
		nil); err != nil {
		return err
	}
	if err := checkInspectLongOptions(args, systemctlReadLongOptions); err != nil {
		return err
	}
	subcommand := args[0].value
	if strings.HasPrefix(subcommand, "-") {
		return fmt.Errorf("systemctl requires a subcommand")
	}
	if !systemctlReadSubcommand(subcommand) {
		return fmt.Errorf("systemctl subcommand %q is not allowed", subcommand)
	}
	return nil
}

func systemctlReadSubcommand(value string) bool {
	switch value {
	case "status", "show", "cat", "list-dependencies":
		return true
	}
	return strings.HasPrefix(value, "list-") || strings.HasPrefix(value, "is-")
}

// validateInspectDocker 只允许 version/info/ps/inspect/top/stats/logs 以及
// image/network/volume/container 的 list/inspect 只读形态。exec/run/attach/
// lifecycle/mutation/pull/push 与 endpoint/context 覆盖在 SSH 前拒绝；
// stats 必须带 --no-stream，logs 必须是非 follow 形态。
func validateInspectDocker(argv []inspectToken) error {
	args := argv[1:]
	if len(args) == 0 {
		return fmt.Errorf("docker requires a subcommand")
	}
	if strings.HasPrefix(args[0].value, "-") {
		return fmt.Errorf("docker global options are not allowed")
	}
	switch args[0].value {
	case "version", "info":
		return validateDockerFlags(args[1:], "f", []string{"--format"}, false)
	case "ps":
		return validateDockerFlags(args[1:], "afqnsl", []string{
			"--all", "--before", "--filter", "--format", "--last", "--latest",
			"--no-trunc", "--quiet", "--since", "--size",
		}, false)
	case "images":
		return validateDockerFlags(args[1:], "afqn", []string{
			"--all", "--digests", "--filter", "--format", "--last", "--no-trunc", "--quiet",
		}, false)
	case "inspect":
		return validateDockerFlags(args[1:], "fs", []string{"--format", "--size", "--type"}, true)
	case "top":
		return validateDockerFlags(args[1:], "u", []string{"--user"}, true)
	case "stats":
		return validateDockerStats(args[1:])
	case "logs":
		return validateDockerLogs(args[1:])
	case "image":
		return validateDockerCollectionSubcommand(args[1:], "image")
	case "network":
		return validateDockerCollectionSubcommand(args[1:], "network")
	case "volume":
		return validateDockerCollectionSubcommand(args[1:], "volume")
	case "container":
		return validateDockerContainerSubcommand(args[1:])
	default:
		return fmt.Errorf("docker subcommand %q is not allowed", args[0].value)
	}
}

// validateDockerFlags 校验 docker 子命令的只读选项：短选项逐字符白名单、长
// 选项精确白名单，超出即拒绝；allowPositionals=false 时额外拒绝位置参数。
func validateDockerFlags(args []inspectToken, shortAllowed string, longAllowed []string, allowPositionals bool) error {
	if err := checkInspectShortOptions(args, shortAllowed); err != nil {
		return err
	}
	if err := checkInspectLongOptions(args, longAllowed); err != nil {
		return err
	}
	if !allowPositionals {
		for _, arg := range args {
			if !strings.HasPrefix(arg.value, "-") {
				return fmt.Errorf("docker positional arguments are not allowed")
			}
		}
	}
	return nil
}

// validateDockerLogs 只允许非 follow 的 docker logs；-f/--follow 是流式读取。
func validateDockerLogs(args []inspectToken) error {
	if err := rejectInspectOptions(args, []string{"-f", "--follow"}, nil); err != nil {
		return err
	}
	return validateDockerFlags(args, "t", []string{
		"--details", "--since", "--tail", "--timestamps", "--until",
	}, true)
}

// validateDockerStats 要求显式 --no-stream；否则远端会持续输出，只能依赖
// harness 超时终止，不能作为可接受的只读形态。
func validateDockerStats(args []inspectToken) error {
	if !containsInspectOption(args, "--no-stream") {
		return fmt.Errorf("docker stats requires --no-stream")
	}
	return validateDockerFlags(args, "a", []string{"--all", "--format", "--no-stream", "--no-trunc"}, true)
}

// validateDockerCollectionSubcommand 校验 image/network/volume 的 ls/inspect 形态。
func validateDockerCollectionSubcommand(args []inspectToken, collection string) error {
	if len(args) == 0 || strings.HasPrefix(args[0].value, "-") {
		return fmt.Errorf("docker %s requires a subcommand", collection)
	}
	switch args[0].value {
	case "ls":
		return validateDockerFlags(args[1:], "afqn", []string{
			"--all", "--digests", "--filter", "--format", "--last", "--no-trunc", "--quiet",
		}, false)
	case "inspect":
		return validateDockerFlags(args[1:], "fs", []string{"--format", "--size", "--type"}, true)
	default:
		return fmt.Errorf("docker %s subcommand %q is not allowed", collection, args[0].value)
	}
}

// validateDockerContainerSubcommand 校验 container 的 ls/inspect/logs/top/stats 形态。
func validateDockerContainerSubcommand(args []inspectToken) error {
	if len(args) == 0 || strings.HasPrefix(args[0].value, "-") {
		return fmt.Errorf("docker container requires a subcommand")
	}
	switch args[0].value {
	case "ls":
		return validateDockerFlags(args[1:], "afqnsl", []string{
			"--all", "--before", "--filter", "--format", "--last", "--latest",
			"--no-trunc", "--quiet", "--since", "--size",
		}, false)
	case "inspect":
		return validateDockerFlags(args[1:], "fs", []string{"--format", "--size", "--type"}, true)
	case "logs":
		return validateDockerLogs(args[1:])
	case "top":
		return validateDockerFlags(args[1:], "u", []string{"--user"}, true)
	case "stats":
		return validateDockerStats(args[1:])
	default:
		return fmt.Errorf("docker container subcommand %q is not allowed", args[0].value)
	}
}
