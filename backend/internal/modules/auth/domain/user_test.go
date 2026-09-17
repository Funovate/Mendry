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
