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
			Name      string `json:"name"`
			Kind      string `json:"kind"`
			ImageKind string `json:"image_kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "catalog"
	}
	parts := make([]string, 0, len(body.Items))
	for _, item := range body.Items {
		label := summaryField(item.Kind)
		if item.ImageKind != "" {
			label += " " + summaryField(item.ImageKind)
		}
		parts = append(parts, summaryField(item.Name)+" ("+label+")")
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
		Container string   `json:"container"`
		Lines     []string `json:"lines"`
		Truncated bool     `json:"truncated"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "program logs"
	}
	head := fmt.Sprintf("%d log lines", len(body.Lines))
	name := body.Unit
	if name == "" {
		name = body.Container
	}
	if name != "" {
		head += " for " + summaryField(name)
	}
	if body.Truncated {
		head += ", log truncated by the host"
	}
	return head
}

func summarizeEntryStatus(payload json.RawMessage) string {
	if s, ok := summarizeImageStatus(payload); ok {
		return s
	}
	return summarizeProgramStatus(payload)
}

func summarizeActed(payload json.RawMessage) string {
	if s, ok := summarizeImageDeploy(payload); ok {
		return s
	}
	return summarizeEntryStatus(payload)
}

// digestShortHex is the hex length shown in text. The structured digest stays
// the full sha256 value so rollback_entry can take it.
const digestShortHex = 12

func shortDigest(d string) string {
	const prefix = "sha256:"
	if strings.HasPrefix(d, prefix) && len(d) == len(prefix)+64 && digestHex(d[len(prefix):]) {
		return prefix + d[len(prefix):len(prefix)+digestShortHex]
	}
	return summaryField(d)
}

func digestHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

const deployLogPreview = 3

// imageStateOK is ImageStatus.state, plus v1.8.1 "unknown" for a deploy or
// rollback whose task ran past its budget. An empty string is not a state.
func imageStateOK(state string) bool {
	switch state {
	case "running", "exited", "created", "paused", "restarting", "dead", "missing", "unknown":
		return true
	default:
		return false
	}
}

func summarizeImageDeploy(payload json.RawMessage) (string, bool) {
	var body struct {
		Deploy struct {
			Outcome  string `json:"outcome"`
			Digest   string `json:"digest"`
			ExitCode *int   `json:"exit_code"`
		} `json:"deploy"`
		Status struct {
			State string `json:"state"`
		} `json:"status"`
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "", false
	}
	if body.Deploy.Outcome == "" && body.Deploy.Digest == "" {
		return "", false
	}
	var b strings.Builder
	if body.Deploy.Outcome != "" {
		b.WriteString(summaryField(body.Deploy.Outcome))
	}
	// v1.8.1: a timed-out deploy or rollback reports state unknown.
	// Other listed states stay off this line so a success summary is unchanged.
	if imageStateOK(body.Status.State) && body.Status.State == "unknown" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString("unknown")
	}
	if body.Deploy.Digest != "" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(shortDigest(body.Deploy.Digest))
	}
	if body.Deploy.ExitCode != nil {
		fmt.Fprintf(&b, " exit %d", *body.Deploy.ExitCode)
	}
	lines := body.Lines
	if len(lines) > deployLogPreview {
		lines = lines[len(lines)-deployLogPreview:]
	}
	for _, line := range lines {
		b.WriteByte('\n')
		b.WriteString(summaryField(redactText(line)))
	}
	return b.String(), true
}

func summarizeImageStatus(payload json.RawMessage) (string, bool) {
	var body struct {
		Container    *string `json:"container"`
		State        string  `json:"state"`
		ExitCode     *int    `json:"exit_code"`
		RestartCount *int    `json:"restart_count"`
		Digest       *string `json:"digest"`
		Unit         string  `json:"unit"`
		ActiveState  string  `json:"active_state"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "", false
	}
	if body.Unit != "" || body.ActiveState != "" || !imageStateOK(body.State) {
		return "", false
	}
	name := "image"
	if body.Container != nil && *body.Container != "" {
		name = summaryField(*body.Container)
	}
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte(' ')
	b.WriteString(summaryField(body.State))
	if body.Digest != nil && *body.Digest != "" {
		b.WriteByte(' ')
		b.WriteString(shortDigest(*body.Digest))
	}
	if body.ExitCode != nil {
		fmt.Fprintf(&b, " exit %d", *body.ExitCode)
	}
	if body.RestartCount != nil {
		fmt.Fprintf(&b, " restarts %d", *body.RestartCount)
	}
	return b.String(), true
}

func historyLines(payload json.RawMessage) string {
	var body struct {
		History []struct {
			Digest  string `json:"digest"`
			Outcome string `json:"outcome"`
			Time    string `json:"time"`
		} `json:"history"`
	}
	if err := json.Unmarshal(payload, &body); err != nil || len(body.History) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nrecent deploys:")
	for _, item := range body.History {
		b.WriteByte('\n')
		b.WriteString(shortDigest(item.Digest))
		if item.Outcome != "" {
			b.WriteByte(' ')
			b.WriteString(summaryField(item.Outcome))
		}
		if item.Time != "" {
			b.WriteByte(' ')
			b.WriteString(summaryField(item.Time))
		}
	}
	return b.String()
}
