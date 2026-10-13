package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const probeServiceSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["service_id"],
  "properties": {
    "service_id": {"type": "string", "format": "uuid"},
    "check_id": {"type": "string", "format": "uuid"},
    "protocol": {"type": "string", "enum": ["http", "tcp"]},
    "port": {"type": "integer", "minimum": 1, "maximum": 65535},
    "path": {"type": "string"}
  }
}`

func registerProbeService(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "probe_service",
		Description: "Probe a service now. This contacts the host now and waits for the agent (up to 30 seconds). Pass check_id to run a saved health check, or protocol (http or tcp), port, and path (required for http, must start with /). Fails if the host is offline or asleep.",
		Annotations: readOnlyAnnotations(true),
		InputSchema: json.RawMessage(probeServiceSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in probeArgs
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		body, err := probeBody(in)
		if err != nil {
			return badRequest(err)
		}
		path := idPath("v1", "services", in.ServiceID, "probe")
		payload, fail := post(ctx, api, path, body)
		if fail != nil {
			return fail
		}
		return present(payload, summarizeProbe(payload))
	})
}

type probeArgs struct {
	ServiceID string  `json:"service_id"`
	CheckID   *string `json:"check_id"`
	Protocol  *string `json:"protocol"`
	Port      *int    `json:"port"`
	Path      *string `json:"path"`
}

func probeBody(in probeArgs) (any, error) {
	if err := requireUUID("service_id", in.ServiceID); err != nil {
		return nil, err
	}
	hasCheck := in.CheckID != nil
	hasAdHoc := in.Protocol != nil || in.Port != nil || in.Path != nil
	if hasCheck && hasAdHoc {
		return nil, errors.New("pass check_id or protocol and port, not both")
	}
	if hasCheck {
		if !uuidPattern.MatchString(*in.CheckID) {
			return nil, errors.New("check_id must be a UUID")
		}
		return struct {
			CheckID string `json:"check_id"`
		}{CheckID: *in.CheckID}, nil
	}
	if in.Protocol == nil || in.Port == nil {
		return nil, errors.New("check_id or protocol and port is required")
	}
	if *in.Protocol != "http" && *in.Protocol != "tcp" {
		return nil, errors.New("protocol must be http or tcp")
	}
	if *in.Port < 1 || *in.Port > 65535 {
		return nil, errors.New("port must be between 1 and 65535")
	}
	body := struct {
		Protocol string  `json:"protocol"`
		Port     int     `json:"port"`
		Path     *string `json:"path"`
	}{Protocol: *in.Protocol, Port: *in.Port}
	switch *in.Protocol {
	case "http":
		if in.Path == nil || !strings.HasPrefix(*in.Path, "/") || strings.ContainsAny(*in.Path, " \t\r\n") {
			return nil, errors.New("path is required for http and must start with /")
		}
		body.Path = in.Path
	case "tcp":
		if in.Path != nil {
			return nil, errors.New("path is not valid for tcp")
		}
		// path stays nil so the JSON body sends "path": null.
	}
	return body, nil
}
