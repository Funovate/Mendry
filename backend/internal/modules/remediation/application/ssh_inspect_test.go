package application

import (
	"testing"
)

func TestParseInspectCommandAcceptsAllowlistedPipeline(t *testing.T) {
	parsed, err := parseInspectCommand(`ls /var/log | grep app`)
	if err != nil {
		t.Fatalf("parseInspectCommand() error = %v", err)
	}
	want := `'ls' '/var/log' | 'grep' 'app'`
	if parsed.Command != want {
		t.Fatalf("reconstructed command = %q, want %q", parsed.Command, want)
	}
}

func TestParseInspectCommandAcceptsQuotedLiteralPatterns(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "quoted character class",
			command: `grep '[0-9]+' app.log`,
			want:    `'grep' '[0-9]+' 'app.log'`,
		},
		{
			name:    "quoted glob characters",
			command: `grep 'foo*' app.log`,
			want:    `'grep' 'foo*' 'app.log'`,
		},
		{
			name:    "escaped glob characters",
			command: `grep foo\* app.log`,
			want:    `'grep' 'foo*' 'app.log'`,
		},
		{
			name:    "double-quoted character class",
			command: `grep "[0-9]+" app.log`,
			want:    `'grep' '[0-9]+' 'app.log'`,
		},
		{
			name:    "quoted find name glob",
			command: `find /var/log -name '*.log'`,
			want:    `'find' '/var/log' '-name' '*.log'`,
		},
		{
			name:    "quoted pipe is a literal argument",
			command: `grep '|' app.log`,
			want:    `'grep' '|' 'app.log'`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := parseInspectCommand(tc.command)
			if err != nil {
				t.Fatalf("parseInspectCommand() error = %v", err)
			}
			if parsed.Command != tc.want {
				t.Fatalf("reconstructed command = %q, want %q", parsed.Command, tc.want)
			}
		})
	}
}

// TestParseInspectCommandAcceptsReadOnlyFamilies 覆盖 PRD R2 要求的常见有界诊断
// 形态：主机身份、网络、进程、文件、系统日志与只读 Docker runtime 状态。
func TestParseInspectCommandAcceptsReadOnlyFamilies(t *testing.T) {
	cases := []string{
		`hostname -I`,
		`hostname -f`,
		`ip addr show`,
		`ip -4 addr show`,
		`ip link show`,
		`ip route show`,
		`ip rule list`,
		`ip neigh show`,
		`ip netns list`,
		`ss -lntp`,
		`ss -tunap`,
		`ps aux`,
		`ps -ef`,
		`cat /etc/os-release`,
		`head -n 5 /var/log/app.log`,
		`tail -n 100 /var/log/app.log`,
		`grep -i error /var/log/app.log`,
		`find /var/log -name '*.log' -mtime -1 -print`,
		`find /srv/app -type f -printf '%p\n'`,
		`stat /srv/app/main.go`,
		`wc -l /var/log/app.log`,
		`journalctl --since '2026-08-24 07:00:00' --until '2026-08-24 07:20:00' -n 50`,
		`journalctl -u checkout-api --since '2026-08-24'`,
		`journalctl --disk-usage`,
		`dmesg -T`,
		`dmesg --level=err`,
		`uname -a`,
		`date -u +%s`,
		`df -h`,
		`du -sh /srv/app`,
		`free -m`,
		`uptime`,
		`id`,
		`lsof -i :8080`,
		`netstat -lntp`,
		`getent hosts checkout-api`,
		`docker version`,
		`docker info`,
		`docker ps`,
		`docker ps -a --no-trunc`,
		`docker inspect checkout-api`,
		`docker inspect --type=container checkout-api`,
		`docker top checkout-api`,
		`docker stats --no-stream checkout-api`,
		`docker logs --since '2026-08-24T07:00:00Z' --until '2026-08-24T07:20:00Z' --tail 100 checkout-api`,
		`docker image ls`,
		`docker image inspect checkout-api:latest`,
		`docker network ls`,
		`docker volume ls`,
		`docker container ls -a`,
		`docker container logs --tail 50 checkout-api`,
		`docker container top checkout-api`,
		`docker container stats --no-stream`,
		`docker container inspect checkout-api`,
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			if _, err := parseInspectCommand(command); err != nil {
				t.Fatalf("expected acceptance for %q: %v", command, err)
			}
		})
	}
}

// TestParseInspectCommandRejectsMutatingVariants 覆盖 PRD AC3：每个混合用途命令族
// 的变异形态都必须被拒绝，包括 hostname <name>、date --set、env <cmd>、
// ss --kill、ip 写操作、find 变异动作、journalctl 状态变更与 Docker 生命周期操作。
func TestParseInspectCommandRejectsMutatingVariants(t *testing.T) {
	cases := []string{
		`hostname new-host`,
		`hostname -F /etc/hostname`,
		`hostname --file /etc/hostname`,
		`hostname --file=/etc/hostname`,
		`hostname -b`,
		`date --set '2026-08-24 12:00:00'`,
		`date -s now`,
		`date -f /tmp/dates.txt`,
		`date --file /tmp/dates.txt`,
		`date --file=/tmp/dates.txt`,
		`date -f/tmp/dates.txt`,
		`env FOO=bar`,
		`env bash -c 'echo hi'`,
		`printenv`,
		`ss --kill`,
		`ss -K`,
		`ss -D /tmp/sockets.txt`,
		`ss --diag=/tmp/sockets.txt`,
		`ip addr add 10.0.0.1/24 dev eth0`,
		`ip addr del 10.0.0.1/24 dev eth0`,
		`ip link set eth0 down`,
		`ip route add default via 10.0.0.1`,
		`ip route flush cache`,
		`ip rule add from 1.2.3.4`,
		`ip neigh add 10.0.0.2 lladdr aa:bb dev eth0`,
		`ip netns exec ns1 bash`,
		`ip monitor`,
		`ip tunnel add tun1 mode gre`,
		`find / -delete`,
		`find . -exec rm {} +`,
		`find . -execdir rm {} +`,
		`find . -ok rm {} +`,
		`find . -fprint /tmp/out.txt`,
		`find . -fprintf /tmp/out.txt '%p\n'`,
		`find . -fls /tmp/out.txt`,
		`journalctl --vacuum-size=100M`,
		`journalctl --rotate`,
		`journalctl --flush`,
		`journalctl --sync`,
		`journalctl --relinquish-var`,
		`journalctl --setup-keys`,
		`journalctl --update-catalog`,
		`dmesg -c`,
		`dmesg --clear`,
		`dmesg -C`,
		`dmesg -n 1`,
		`dmesg --console-off`,
		`systemctl start checkout-api`,
		`systemctl stop checkout-api`,
		`systemctl restart checkout-api`,
		`systemctl reload checkout-api`,
		`systemctl enable checkout-api`,
		`systemctl disable checkout-api`,
		`systemctl mask checkout-api`,
		`systemctl daemon-reload`,
		`systemctl edit checkout-api`,
		`systemctl set-property checkout-api CPUQuota=50%`,
		`systemctl -H host1 status checkout-api`,
		`docker exec checkout-api bash`,
		`docker run -d nginx`,
		`docker attach checkout-api`,
		`docker cp /etc/passwd checkout-api:/tmp/x`,
		`docker start checkout-api`,
		`docker stop checkout-api`,
		`docker restart checkout-api`,
		`docker kill checkout-api`,
		`docker rm checkout-api`,
		`docker rm -f checkout-api`,
		`docker update --memory 1g checkout-api`,
		`docker pause checkout-api`,
		`docker unpause checkout-api`,
		`docker rename checkout-api checkout-api2`,
		`docker wait checkout-api`,
		`docker commit checkout-api snapshot`,
		`docker build -t app .`,
		`docker pull nginx`,
		`docker push repo/app`,
		`docker prune`,
		`docker system prune -a`,
		`docker image rm nginx`,
		`docker network rm net1`,
		`docker volume rm vol1`,
		`docker container rm checkout-api`,
		`docker container run nginx`,
		`docker logs -f checkout-api`,
		`docker logs --follow checkout-api`,
		`docker stats checkout-api`,
		`docker -H tcp://evil:2375 ps`,
		`docker --context prod ps`,
		`docker --host tcp://evil:2375 version`,
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			if _, err := parseInspectCommand(command); err == nil {
				t.Fatalf("expected rejection for %q", command)
			}
		})
	}
}

// TestParseInspectCommandRejectsUnsafeSyntax 覆盖 PRD AC4：shell 替换、重定向、
// 分隔符、不支持管道、解释器、提权与嵌套命令执行都在 SSH 前拒绝。
func TestParseInspectCommandRejectsUnsafeSyntax(t *testing.T) {
	cases := []string{
		`ls; rm -rf /`,
		`cat $(pwd)`,
		`sudo journalctl`,
		`su - root`,
		`bash -c 'ls'`,
		`sh -c 'ls'`,
		`python3 -c 'print(1)'`,
		`perl -e 'print 1'`,
		`node -e 'console.log(1)'`,
		`xargs rm`,
		`echo hi > file`,
		`FOO=bar ls`,
		`ls && cat /etc/passwd`,
		`ls || cat /etc/passwd`,
		`cat ` + "`pwd`",
		`ls /var/log/../etc`,
		`ls *.log`,
		`ls /var/log/*.log`,
		`find /var/log -name *.log`,
		`grep [0-9]+ app.log`,
		`ls /var/log | grep app | wc -l | cat`,
		`find /tmp -delete`,
		`tail -f /var/log/app.log`,
		`tail --follow /var/log/app.log`,
		`tail --follow=name /var/log/app.log`,
		`ls --color=auto ; rm -rf /`,
		`docker ps | docker exec -i checkout-api sh`,
		`ls | bash`,
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			if _, err := parseInspectCommand(command); err == nil {
				t.Fatalf("expected rejection for %q", command)
			}
		})
	}
}

// TestParseInspectCommandValidatesEveryPipelineSegment 覆盖 PRD AC5：最多三段管道
// 只在每一段都独立通过命令策略时可用；任意一段不合格都整体拒绝。
func TestParseInspectCommandValidatesEveryPipelineSegment(t *testing.T) {
	accepted := []string{
		`ls /var/log | grep app | head -n 5`,
		`ps aux | grep checkout | wc -l`,
		`ss -lntp | grep 8080`,
		`docker ps --no-trunc | grep checkout-api`,
		`journalctl -u checkout-api --since '2026-08-24' | grep -i error`,
	}
	for _, command := range accepted {
		t.Run("accept "+command, func(t *testing.T) {
			if _, err := parseInspectCommand(command); err != nil {
				t.Fatalf("expected acceptance for %q: %v", command, err)
			}
		})
	}
	rejected := []string{
		`ps aux | sudo kill 1`,
		`ls /var/log | docker exec checkout-api sh`,
		`cat /etc/passwd | env`,
		`ls /var/log | grep app | wc -l | cat`,
		`docker ps | journalctl --vacuum-size=1M`,
		`ip addr show | ss -K`,
	}
	for _, command := range rejected {
		t.Run("reject "+command, func(t *testing.T) {
			if _, err := parseInspectCommand(command); err == nil {
				t.Fatalf("expected rejection for %q", command)
			}
		})
	}
}

// TestParseInspectCommandRejectsCombinedShortFlagMutation 证明组合短选项无法绕过
// 逐字符白名单：-fb（follow+boot）、-Kt（kill+tcp）、-IsF（hostname 组合 + -F）。
func TestParseInspectCommandRejectsCombinedShortFlagMutation(t *testing.T) {
	cases := []string{
		`journalctl -fb`,
		`journalctl -xq -fb`,
		`ss -Kt`,
		`ss -lanp -K`,
		`hostname -IsF /etc/hostname`,
		`hostname -fF`,
		`date -us`,
		`dmesg -wc`,
		`tail -fn 10 /var/log/app.log`,
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			if _, err := parseInspectCommand(command); err == nil {
				t.Fatalf("expected rejection for %q", command)
			}
		})
	}
}
