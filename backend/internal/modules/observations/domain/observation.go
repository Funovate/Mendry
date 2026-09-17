package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type Observation struct {
	ID            string
	ProjectID     string
	EnvironmentID string
	SourceID      string
	Service       *string
	OccurredAt    time.Time
	Level         string
	Message       string
	Host          *string
	RequestID     *string
	Fingerprint   string
	Attributes    json.RawMessage
	IngestedAt    time.Time
}

func (o Observation) Validate() error {
	if o.ID == "" || o.ProjectID == "" || o.EnvironmentID == "" || o.SourceID == "" || o.OccurredAt.IsZero() {
		return fmt.Errorf("observation identity and time are required")
	}
	if !oneOf(o.Level, "debug", "info", "warn", "error", "critical") || !bounded(o.Message, 1, 65536) ||
		!bounded(o.Fingerprint, 1, 255) || (o.Service != nil && !bounded(*o.Service, 1, 120)) ||
		(o.Host != nil && !bounded(*o.Host, 1, 255)) || (o.RequestID != nil && !bounded(*o.RequestID, 1, 255)) {
		return fmt.Errorf("observation fields are invalid")
	}
	if err := validateAttributes(o.Attributes); err != nil {
		return err
	}
	return nil
}

func validateAttributes(raw []byte) error {
	if len(raw) == 0 || len(raw) > 16384 {
		return fmt.Errorf("observation attributes size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return fmt.Errorf("observation attributes must be a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("observation attributes must contain one object")
	}
	allowed := map[string]struct{}{
		"component": {}, "logger": {}, "traceId": {}, "spanId": {}, "region": {}, "zone": {},
	}
	for key, item := range value {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("observation attribute is not allowed")
		}
		switch item.(type) {
		case string, float64, bool, nil:
		default:
			return fmt.Errorf("observation attribute value must be scalar")
		}
	}
	return nil
}

func bounded(value string, minimum, maximum int) bool {
	length := len(strings.TrimSpace(value))
	return length >= minimum && length <= maximum
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
