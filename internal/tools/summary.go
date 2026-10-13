package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

const previewCount = 8

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
		parts = append(parts, p.Name+" ("+p.Role+")")
	}
	return withPreview(fmt.Sprintf("%d projects", len(body.Projects)), parts)
}

func summarizeHosts(payload json.RawMessage) string {
	var body struct {
		Items []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "hosts"
	}
	parts := make([]string, 0, len(body.Items))
	for _, h := range body.Items {
		parts = append(parts, h.Name+" "+h.Status)
	}
	return withPreview(fmt.Sprintf("%d hosts", len(body.Items)), parts)
}

func summarizeServices(payload json.RawMessage) string {
	var body struct {
		Items []struct {
			Name   string `json:"name"`
			Health struct {
				Status string `json:"status"`
			} `json:"health"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "services"
	}
	parts := make([]string, 0, len(body.Items))
	for _, s := range body.Items {
		status := s.Health.Status
		if status == "" {
			status = "unknown"
		}
		parts = append(parts, s.Name+" "+status)
	}
	return withPreview(fmt.Sprintf("%d services", len(body.Items)), parts)
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
	return fmt.Sprintf("%s: %d series, %d points", metric, len(body.Series), points)
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
		names = append(names, item.ProcessName)
	}
	head := fmt.Sprintf("%d processes", len(body.Items))
	if body.SampledAt != "" {
		head += " at " + body.SampledAt
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
	return fmt.Sprintf("snapshot at %s, cpu %.4g%%", body.SampledAt, body.CPUPercent)
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
	return fmt.Sprintf("probe %s %s:%d (%s) %dms", body.Result, body.Protocol, body.Port, body.Service, body.LatencyMS)
}
