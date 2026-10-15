package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const previewCount = 8

// summaryFieldRunes is the longest single field placed in a summary line.
const summaryFieldRunes = 80

// maxPlanRunes is the longest confirmation plan placed in a tool result.
// The plan is the sentence the user must agree to, so it is longer than one field.
const maxPlanRunes = 1024

// summaryField replaces controls, format characters, and line or paragraph
// separators with U+FFFD, then caps the result at summaryFieldRunes.
// That keeps bidi overrides and Unicode line breaks out of a summary line.
func summaryField(s string) string {
	return clampField(s, summaryFieldRunes)
}

// clampField is summaryField with an explicit rune cap.
func clampField(s string, max int) string {
	runes := make([]rune, 0, len(s))
	for _, r := range s {
		if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) {
			r = '\uFFFD'
		}
		runes = append(runes, r)
	}
	if max < 1 {
		return ""
	}
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max-1]) + "…"
}

func withPreview(head string, parts []string) string {
	shown := preview(parts)
	if shown == "" {
		return head
	}
	return head + ": " + shown
}

func preview(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) <= previewCount {
		return strings.Join(parts, ", ")
	}
	rest := len(parts) - previewCount
	return strings.Join(parts[:previewCount], ", ") + fmt.Sprintf(", and %d more", rest)
}

func summarizeProjects(payload json.RawMessage) string {
	var body struct {
		Projects []struct {
			Name string `json:"name"`
			Role string `json:"role"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "projects"
	}
	parts := make([]string, 0, len(body.Projects))
	for _, p := range body.Projects {
		parts = append(parts, summaryField(p.Name)+" ("+summaryField(p.Role)+")")
	}
	return withPreview(fmt.Sprintf("%d projects", len(body.Projects)), parts)
}

func summarizeHosts(payload json.RawMessage) string {
	var body struct {
		Items []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Relation   string `json:"relation"`
			ShareLevel string `json:"share_level"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "hosts"
	}
	parts := make([]string, 0, len(body.Items))
	for _, h := range body.Items {
		line := summaryField(h.Name) + " " + summaryField(h.Status)
		if h.Relation == "shared" {
			line += " (shared: " + summaryField(h.ShareLevel) + ")"
		}
		parts = append(parts, line)
	}
	return withPreview(fmt.Sprintf("%d hosts", len(body.Items)), parts)
}

// portServiceName is the discovered name the v1.2 label rewrites.
var portServiceName = regexp.MustCompile(`^port-(\d+)$`)

// serviceLabel applies the v1.2 rule: display_name, else name, unless name
// is port-<n>, which becomes "port <n> · <owner_user>" (or "port <n>").
func serviceLabel(displayName, name, ownerUser string) string {
	if displayName != "" {
		return summaryField(displayName)
	}
	m := portServiceName.FindStringSubmatch(name)
	if m == nil {
		return summaryField(name)
	}
	if ownerUser == "" {
		return "port " + summaryField(m[1])
	}
	return "port " + summaryField(m[1]) + " · " + summaryField(ownerUser)
}

func summarizeServices(payload json.RawMessage, project bool) string {
	var body struct {
		Items []struct {
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
			OwnerUser   string `json:"owner_user"`
			HostName    string `json:"host_name"`
			Health      struct {
				Status string `json:"status"`
			} `json:"health"`
			GoneAt *string `json:"gone_at"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
		Total      *int    `json:"total"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "services"
	}
	n := len(body.Items)
	if project && body.Total != nil {
		n = *body.Total
	}
	head := fmt.Sprintf("%d services", n)
	if project {
		if body.NextCursor != nil && *body.NextCursor != "" {
			head += ", more pages"
		} else {
			head += ", no more pages"
		}
	}
	items := body.Items
	if len(items) > maxListItems {
		items = items[:maxListItems]
	}
	lines := make([]string, 0, len(items)+1)
	lines = append(lines, head)
	for _, s := range items {
		status := summaryField(s.Health.Status)
		if status == "" {
			status = "unknown"
		}
		line := serviceLabel(s.DisplayName, s.Name, s.OwnerUser) + " " + status
		host := summaryField(s.HostName)
		if project && host != "" {
			line += " " + host
		}
		// A non-empty gone_at is when the host stopped reporting the service.
		// Null stays off the line; the health status is still the status.
		if s.GoneAt != nil && *s.GoneAt != "" {
			line += " (gone_at " + summaryField(*s.GoneAt) + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func summarizeMetrics(metric string, payload json.RawMessage) string {
	var body struct {
		Series []struct {
			Points []json.RawMessage `json:"points"`
		} `json:"series"`
	}
	_ = json.Unmarshal(payload, &body)
	points := 0
	for _, s := range body.Series {
		points += len(s.Points)
	}
	return fmt.Sprintf("%s: %d series, %d points", summaryField(metric), len(body.Series), points)
}

func summarizeProcesses(payload json.RawMessage) string {
	var body struct {
		SampledAt string `json:"sampled_at"`
		Items     []struct {
			ProcessName string `json:"process_name"`
		} `json:"items"`
	}
	_ = json.Unmarshal(payload, &body)
	names := make([]string, 0, len(body.Items))
	for _, item := range body.Items {
		names = append(names, summaryField(item.ProcessName))
	}
	head := fmt.Sprintf("%d processes", len(body.Items))
	if body.SampledAt != "" {
		head += " at " + summaryField(body.SampledAt)
	}
	return withPreview(head, names)
}

func summarizeHealth(payload json.RawMessage) string {
	var body struct {
		Items []json.RawMessage `json:"items"`
	}
	_ = json.Unmarshal(payload, &body)
	return fmt.Sprintf("%d health results", len(body.Items))
}

func summarizeSnapshot(payload json.RawMessage) string {
	var body struct {
		SampledAt  string  `json:"sampled_at"`
		CPUPercent float64 `json:"cpu_percent"`
	}
	if err := json.Unmarshal(payload, &body); err != nil || body.SampledAt == "" {
		return "host snapshot"
	}
	return fmt.Sprintf("snapshot at %s, cpu %.4g%%", summaryField(body.SampledAt), body.CPUPercent)
}

func summarizeProbe(payload json.RawMessage) string {
	var body struct {
		Service   string `json:"service"`
		Protocol  string `json:"protocol"`
		Port      int    `json:"port"`
		Result    string `json:"result"`
		LatencyMS int    `json:"latency_ms"`
	}
	if err := json.Unmarshal(payload, &body); err != nil || body.Result == "" {
		return "probe result"
	}
	return fmt.Sprintf("probe %s %s:%d (%s) %dms", summaryField(body.Result), summaryField(body.Protocol), body.Port, summaryField(body.Service), body.LatencyMS)
}

func summarizeCatalog(payload json.RawMessage) string {
	var body struct {
		Items []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "catalog"
	}
	parts := make([]string, 0, len(body.Items))
	for _, item := range body.Items {
		parts = append(parts, summaryField(item.Name)+" ("+summaryField(item.Kind)+")")
	}
	return withPreview(fmt.Sprintf("%d catalog entries", len(body.Items)), parts)
}

func summarizeProgramStatus(payload json.RawMessage) string {
	var body struct {
		Unit        string `json:"unit"`
		ActiveState string `json:"active_state"`
		SubState    string `json:"sub_state"`
		MainPID     *int   `json:"main_pid"`
	}
	if err := json.Unmarshal(payload, &body); err != nil || (body.Unit == "" && body.ActiveState == "") {
		return "program status"
	}
	head := summaryField(body.Unit)
	if head == "" {
		head = "program"
	}
	if body.ActiveState != "" {
		head += " " + summaryField(body.ActiveState)
	}
	if body.SubState != "" {
		head += " (" + summaryField(body.SubState) + ")"
	}
	if body.MainPID != nil {
		head += fmt.Sprintf(" pid %d", *body.MainPID)
	}
	return head
}

func summarizeProgramLogs(payload json.RawMessage) string {
	var body struct {
		Unit      string   `json:"unit"`
		Lines     []string `json:"lines"`
		Truncated bool     `json:"truncated"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "program logs"
	}
	head := fmt.Sprintf("%d log lines", len(body.Lines))
	if body.Unit != "" {
		head += " for " + summaryField(body.Unit)
	}
	if body.Truncated {
		head += ", log truncated by the host"
	}
	return head
}
