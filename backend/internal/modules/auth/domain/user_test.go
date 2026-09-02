package domain

import "testing"

func TestNormalizeUsername(t *testing.T) {
	username, err := NormalizeUsername("  Admin.User  ")
	if err != nil || username != "admin.user" {
		t.Fatalf("NormalizeUsername() = %q, %v", username, err)
	}
	for _, value := range []string{"ab", "1admin", "admin user", "admin@example.com"} {
		if _, err := NormalizeUsername(value); err == nil {
			t.Errorf("NormalizeUsername(%q) error = nil", value)
		}
	}
}

func TestParseRole(t *testing.T) {
	for _, value := range []string{"admin", "operator", "viewer"} {
		if role, err := ParseRole(value); err != nil || string(role) != value {
			t.Fatalf("ParseRole(%q) = %q, %v", value, role, err)
		}
	}
	if _, err := ParseRole("owner"); err == nil {
		t.Fatal("ParseRole(owner) error = nil")
	}
}
