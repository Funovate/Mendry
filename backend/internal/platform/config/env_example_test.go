package config

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var exampleAssignment = regexp.MustCompile(`^#?\s*(MENDRY_[A-Z0-9_]+)=`)

func TestEnvironmentExampleDocumentsEveryConfigurationKey(t *testing.T) {
	runtimeKeys := configurationKeysFromSource(t)
	exampleKeys := environmentExampleKeys(t)
	testKeys := map[string]struct{}{
		"MENDRY_TEST_POSTGRES_URL":       {},
		"MENDRY_TEST_POSTGRES_ISOLATION": {},
		"MENDRY_TEST_REDIS_URL":          {},
		"MENDRY_TEST_REDIS_PREFIX":       {},
	}

	for key := range runtimeKeys {
		if count := exampleKeys[key]; count != 1 {
			t.Errorf(".env.example assignment count for %s = %d, want 1", key, count)
		}
	}
	for key, count := range exampleKeys {
		_, isRuntimeKey := runtimeKeys[key]
		_, isTestKey := testKeys[key]
		if !isRuntimeKey && !isTestKey {
			t.Errorf(".env.example contains unsupported runtime key %s", key)
		}
		if count != 1 {
			t.Errorf(".env.example assignment count for %s = %d, want 1", key, count)
		}
	}
	for key := range testKeys {
		if count := exampleKeys[key]; count != 1 {
			t.Errorf(".env.example assignment count for %s = %d, want 1", key, count)
		}
	}
}

func configurationKeysFromSource(t *testing.T) map[string]struct{} {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), "config.go", nil, 0)
	if err != nil {
		t.Fatalf("parse config.go: %v", err)
	}

	keys := make(map[string]struct{})
	ast.Inspect(parsed, func(node ast.Node) bool {
		declaration, ok := node.(*ast.GenDecl)
		if !ok || declaration.Tok != token.CONST {
			return true
		}
		for _, specification := range declaration.Specs {
			valueSpec, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range valueSpec.Names {
				if !strings.HasSuffix(name.Name, "Key") || index >= len(valueSpec.Values) {
					continue
				}
				literal, ok := valueSpec.Values[index].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(literal.Value)
				if err == nil && strings.HasPrefix(value, "MENDRY_") {
					keys[value] = struct{}{}
				}
			}
		}
		return false
	})
	return keys
}

func environmentExampleKeys(t *testing.T) map[string]int {
	t.Helper()

	file, err := os.Open("../../../.env.example")
	if err != nil {
		t.Fatalf("open .env.example: %v", err)
	}
	defer file.Close()

	keys := make(map[string]int)
	previousLine := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		matches := exampleAssignment.FindStringSubmatch(line)
		if matches != nil {
			key := matches[1]
			keys[key]++
			if !strings.HasPrefix(previousLine, "#") {
				t.Errorf(".env.example key %s has no adjacent comment", key)
			}
		}
		previousLine = line
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	return keys
}
