package tools

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// uuidPattern matches the canonical 8-4-4-4-12 hex form. Version and variant
// nibbles are not constrained: the API accepts any UUID string.
var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const maxMetricSpan = 31 * 24 * time.Hour

var metricNames = map[string]bool{
	"cpu": true, "mem": true, "disk": true, "diskio": true,
	"net": true, "system": true, "temp": true,
}

func requireUUID(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if !uuidPattern.MatchString(value) {
		return fmt.Errorf("%s must be a UUID", name)
	}
	return nil
}

func optionalTime(name string, value *string) (string, error) {
	if value == nil {
		return "", nil
	}
	if *value == "" {
		return "", fmt.Errorf("%s must be RFC3339 UTC", name)
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return "", fmt.Errorf("%s must be RFC3339 UTC", name)
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return "", fmt.Errorf("%s must be RFC3339 UTC", name)
	}
	return *value, nil
}

func requireSpan(from, to string, limit time.Duration) error {
	if from == "" || to == "" {
		return nil
	}
	start, err := time.Parse(time.RFC3339, from)
	if err != nil {
		return errors.New("from must be RFC3339 UTC")
	}
	end, err := time.Parse(time.RFC3339, to)
	if err != nil {
		return errors.New("to must be RFC3339 UTC")
	}
	if end.Before(start) {
		return errors.New("from must not be after to")
	}
	if limit > 0 && end.Sub(start) > limit {
		return errors.New("from and to span more than 31 days")
	}
	return nil
}
