package application

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/projects/domain"
)

var ErrLogRuleTrialUnsupported = errors.New("rule regex is not supported by the trial matcher")

type LogRuleTrialInput struct {
	Config   domain.CustomRuleConfig `json:"config"`
	Positive []string                `json:"positive"`
	Negative []string                `json:"negative"`
}

type LogRuleTrialResult struct {
	RuleID           string `json:"ruleId"`
	PositiveMatches  []int  `json:"positiveMatches"`
	NegativeMatches  []int  `json:"negativeMatches"`
	PositiveExcluded []int  `json:"positiveExcluded"`
	NegativeExcluded []int  `json:"negativeExcluded"`
}

type LogRuleTrial struct {
	PositiveCount int                  `json:"positiveCount"`
	NegativeCount int                  `json:"negativeCount"`
	Rules         []LogRuleTrialResult `json:"rules"`
}

func (s *Service) TrialLogRules(ctx context.Context, principal authdomain.User, projectKey string, input LogRuleTrialInput) (LogRuleTrial, error) {
	if _, err := s.resolveProject(ctx, principal, projectKey); err != nil {
		return LogRuleTrial{}, err
	}
	if len(input.Positive) > 100 || len(input.Negative) > 100 || len(input.Positive)+len(input.Negative) == 0 {
		return LogRuleTrial{}, ErrInvalidInput
	}
	bytes := 0
	for _, line := range append(append([]string{}, input.Positive...), input.Negative...) {
		bytes += len(line)
		if bytes > 65536 || strings.ContainsAny(line, "\r\n") || !utf8.ValidString(line) {
			return LogRuleTrial{}, ErrInvalidInput
		}
	}
	encoded, err := json.Marshal(input.Config)
	if err != nil {
		return LogRuleTrial{}, ErrInvalidInput
	}
	config, err := domain.ParseCustomRuleConfig(encoded)
	if err != nil {
		return LogRuleTrial{}, ErrInvalidInput
	}
	result := LogRuleTrial{PositiveCount: len(input.Positive), NegativeCount: len(input.Negative), Rules: make([]LogRuleTrialResult, 0, len(config.Rules))}
	for _, rule := range config.Rules {
		var include, exclude *regexp.Regexp
		if rule.MatchType == "regex" {
			include, err = compileTrialRegex(rule.Pattern)
			if err != nil {
				return LogRuleTrial{}, ErrLogRuleTrialUnsupported
			}
		}
		if rule.ExcludePattern != "" {
			exclude, err = compileTrialRegex(rule.ExcludePattern)
			if err != nil {
				return LogRuleTrial{}, ErrLogRuleTrialUnsupported
			}
		}
		matched := LogRuleTrialResult{RuleID: rule.ID, PositiveMatches: []int{}, NegativeMatches: []int{}, PositiveExcluded: []int{}, NegativeExcluded: []int{}}
		for _, group := range []struct {
			lines    []string
			matches  *[]int
			excluded *[]int
		}{{input.Positive, &matched.PositiveMatches, &matched.PositiveExcluded}, {input.Negative, &matched.NegativeMatches, &matched.NegativeExcluded}} {
			for index, raw := range group.lines {
				line := strings.ToValidUTF8(string([]byte(raw)[:min(len(raw), 4096)]), "")
				if strings.TrimSpace(line) == "" {
					continue
				}
				baseMatch := (include != nil && include.MatchString(line)) || (include == nil && strings.Contains(line, rule.Pattern))
				if !baseMatch {
					continue
				}
				if exclude != nil && exclude.MatchString(line) {
					*group.excluded = append(*group.excluded, index+1)
				} else {
					*group.matches = append(*group.matches, index+1)
				}
			}
		}
		result.Rules = append(result.Rules, matched)
	}
	return result, nil
}

// Preview only the common subset: Python re and Go regexp differ on shorthand
// character classes, lookarounds, inline flags and backreferences.
func compileTrialRegex(pattern string) (*regexp.Regexp, error) {
	if strings.Contains(pattern, "[[:") || strings.Contains(pattern, "&&") || strings.Contains(pattern, "--") {
		return nil, ErrLogRuleTrialUnsupported
	}
	for index := 0; index < len(pattern); index++ {
		if pattern[index] == '(' && index+1 < len(pattern) && pattern[index+1] == '?' {
			return nil, ErrLogRuleTrialUnsupported
		}
		if pattern[index] == '\\' {
			index++
			if index == len(pattern) || !strings.ContainsRune(`.[]{}()*+?|^$\-`, rune(pattern[index])) {
				return nil, ErrLogRuleTrialUnsupported
			}
		}
	}
	return regexp.Compile(pattern)
}
