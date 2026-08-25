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

func TestParseInspectCommandRejectsUnsafeSyntax(t *testing.T) {
	cases := []string{
		`ls; rm -rf /`,
		`cat $(pwd)`,
		`sudo journalctl`,
		`find . -exec rm {} +`,
		`journalctl -f`,
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
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			if _, err := parseInspectCommand(command); err == nil {
				t.Fatalf("expected rejection for %q", command)
			}
		})
	}
}
