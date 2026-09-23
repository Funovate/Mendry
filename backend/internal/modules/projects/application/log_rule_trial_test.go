package application

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/projects/domain"
)

func trialRule() domain.CustomRule {
	return domain.CustomRule{ID: "errors", Name: "Errors", MatchType: "contains", Pattern: "ERROR", ExcludePattern: "healthcheck", Threshold: 2, WindowSeconds: 60, CooldownSeconds: 300}
}

func trialInput(rule domain.CustomRule) LogRuleTrialInput {
	return LogRuleTrialInput{Config: domain.CustomRuleConfig{SchemaVersion: 2, GroupingWindowSeconds: 300, Rules: []domain.CustomRule{rule}}}
}

func TestTrialLogRulesMatchesProbeLineSemantics(t *testing.T) {
	service := newService(t, &fakeRepository{project: domain.Project{ID: projectID, Key: "payments"}}, &fakeCipher{})
	input := trialInput(trialRule())
	input.Positive = []string{"ERROR payment", "ERROR healthcheck", "error payment", "\t", strings.Repeat("x", 4096) + "ERROR"}
	input.Negative = []string{"INFO payment", "ERROR healthcheck", "ERROR unexpected"}
	result, err := service.TrialLogRules(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", input)
	if err != nil {
		t.Fatal(err)
	}
	if result.PositiveCount != 5 || result.NegativeCount != 3 || len(result.Rules) != 1 {
		t.Fatalf("result = %#v", result)
	}
	matched := result.Rules[0]
	if !reflect.DeepEqual(matched.PositiveMatches, []int{1}) || !reflect.DeepEqual(matched.PositiveExcluded, []int{2}) ||
		!reflect.DeepEqual(matched.NegativeMatches, []int{3}) || !reflect.DeepEqual(matched.NegativeExcluded, []int{2}) {
		t.Fatalf("match detail = %#v", matched)
	}
}

func TestTrialRegexMatchesPythonOnSupportedPatterns(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required for matching parity test")
	}
	lines := []string{"ERROR payment", "WARN 42", "foo.bar", "error", "FAILED", "错误 payment", "XY", "fooXbar"}
	for _, pattern := range []string{`ERROR.*payment`, `^WARN [0-9]+$`, `error|FAILED`, `foo\.bar`, `[A-Z]{2,4}`, `错误`} {
		compiled, err := compileTrialRegex(pattern)
		if err != nil {
			t.Fatalf("compile %q: %v", pattern, err)
		}
		input, _ := json.Marshal(map[string]any{"pattern": pattern, "lines": lines})
		command := exec.Command(python, "-c", `import json, re, sys
data = json.load(sys.stdin)
json.dump([bool(re.search(data["pattern"], line)) for line in data["lines"]], sys.stdout)`)
		command.Stdin = strings.NewReader(string(input))
		output, err := command.Output()
		if err != nil {
			t.Fatalf("python matcher: %v", err)
		}
		var want []bool
		if err := json.Unmarshal(output, &want); err != nil {
			t.Fatal(err)
		}
		for index, line := range lines {
			if compiled.MatchString(line) != want[index] {
				t.Fatalf("pattern %q line %q: Go/Python mismatch", pattern, line)
			}
		}
	}
}

func TestTrialLogRulesRejectsUnsupportedRegexAndOversizedSamples(t *testing.T) {
	service := newService(t, &fakeRepository{project: domain.Project{ID: projectID, Key: "payments"}}, &fakeCipher{})
	user := authdomain.User{ID: userID, Enabled: true}
	input := trialInput(trialRule())
	input.Positive = []string{"ERROR"}
	input.Config.Rules[0].MatchType = "regex"
	input.Config.Rules[0].Pattern = `\w+`
	if _, err := service.TrialLogRules(context.Background(), user, "payments", input); !errors.Is(err, ErrLogRuleTrialUnsupported) {
		t.Fatalf("shorthand regex error = %v", err)
	}
	input.Config.Rules[0].Pattern = `ERROR.*payment`
	result, err := service.TrialLogRules(context.Background(), user, "payments", input)
	if err != nil || len(result.Rules[0].PositiveMatches) != 0 {
		t.Fatalf("regex result = %#v, %v", result, err)
	}
	input.Positive = []string{strings.Repeat("E", 65537)}
	if _, err := service.TrialLogRules(context.Background(), user, "payments", input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized sample error = %v", err)
	}
	input.Positive = []string{"ERROR"}
	if _, err := service.TrialLogRules(context.Background(), authdomain.User{}, "payments", input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unauthorized trial error = %v", err)
	}
}
