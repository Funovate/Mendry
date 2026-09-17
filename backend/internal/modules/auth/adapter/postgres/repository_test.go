package postgres

import "testing"

func TestUUIDParameterRequiresVersionSeven(t *testing.T) {
	valid, err := uuidParameter("019ff544-405c-7d10-8f10-cb3fc579605c")
	if err != nil || !valid.Valid {
		t.Fatalf("uuidParameter(v7) = %#v, %v", valid, err)
	}
	for _, value := range []string{"not-a-uuid", "550e8400-e29b-41d4-a716-446655440000"} {
		if _, err := uuidParameter(value); err == nil {
			t.Errorf("uuidParameter(%q) error = nil", value)
		}
	}
}
