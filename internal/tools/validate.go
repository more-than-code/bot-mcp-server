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

// entryNamePattern is the catalog name rule from api-v1 v1.6.
var entryNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

// idempotencyKeyPattern is the v1.6 key: 1–128 chars from that set.
var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// confirmationIDPattern is the 43-character base64url confirmation id.
var confirmationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func requireEntryName(value string) error {
	if value == "" {
		return errors.New("entry is required")
	}
	if !entryNamePattern.MatchString(value) {
		return errors.New("entry must be a lowercase catalog name of at most 48 characters")
	}
	return nil
}

func requireIdempotencyKey(value string) error {
	if value == "" {
		return errors.New("idempotency_key is required")
	}
	if !idempotencyKeyPattern.MatchString(value) {
		return errors.New("idempotency_key must be 1 to 128 characters from [A-Za-z0-9._:-]")
	}
	return nil
}

func requireConfirmationID(value string) error {
	if value == "" {
		return errors.New("confirmation_id is required")
	}
	if !confirmationIDPattern.MatchString(value) {
		return errors.New("confirmation_id must be 43 base64url characters")
	}
	return nil
}

func optionalLines(value *int) error {
	if value == nil {
		return nil
	}
	if *value < 1 || *value > 1000 {
		return errors.New("lines must be between 1 and 1000")
	}
	return nil
}

func validateEntryLocation(projectID, entry, hostID string) error {
	if err := requireUUID("project_id", projectID); err != nil {
		return err
	}
	if err := requireEntryName(entry); err != nil {
		return err
	}
	return requireUUID("host_id", hostID)
}

func validateEntryAction(projectID, entry, hostID, key string) error {
	if err := validateEntryLocation(projectID, entry, hostID); err != nil {
		return err
	}
	return requireIdempotencyKey(key)
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
