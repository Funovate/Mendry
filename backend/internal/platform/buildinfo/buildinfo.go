// Package buildinfo 提供编译期注入且可安全写入日志的构建身份信息。
package buildinfo

// Version、Commit 和 BuildDate 由 release 构建通过 linker flags 注入。
// 开发构建保留安全默认值，避免日志身份字段为空。
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Info 是进程启动后使用的不可变构建身份快照。
type Info struct {
	Version   string
	Commit    string
	BuildDate string
}

// Current 返回当前二进制的构建身份，并为空的 linker flag 提供安全默认值。
func Current() Info {
	return Info{
		Version:   valueOrDefault(Version, "dev"),
		Commit:    valueOrDefault(Commit, "unknown"),
		BuildDate: valueOrDefault(BuildDate, "unknown"),
	}
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
