package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

// listServicesSchema states the bounds JSON Schema can. Exactly one of
// host_id or project_id, and the query fields only with project_id, are
// also checked in code: clients do not all enforce oneOf.
const listServicesSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "host_id": {"type": "string", "format": "uuid"},
    "project_id": {"type": "string", "format": "uuid"},
    "q": {"type": "string", "maxLength": 100},
    "sort": {"type": "string", "enum": ["name", "host", "kind", "status", "last_seen"]},
    "dir": {"type": "string", "enum": ["asc", "desc"]},
    "limit": {"type": "integer", "minimum": 1, "maximum": 200},
    "cursor": {"type": "string"},
    "view": {"type": "string", "enum": ["all", "deploybot", "deploybot_and_discovered"]}
  },
  "oneOf": [
    {"required": ["host_id"]},
    {"required": ["project_id"]}
  ],
  "if": {"required": ["host_id"]},
  "then": {
    "not": {
      "anyOf": [
        {"required": ["q"]},
        {"required": ["sort"]},
        {"required": ["dir"]},
        {"required": ["limit"]},
        {"required": ["cursor"]},
        {"required": ["view"]}
      ]
    }
  }
}`

type listServicesArgs struct {
	HostID    *string `json:"host_id"`
	ProjectID *string `json:"project_id"`
	Q         *string `json:"q"`
	Sort      *string `json:"sort"`
	Dir       *string `json:"dir"`
	Limit     *int    `json:"limit"`
	Cursor    *string `json:"cursor"`
	View      *string `json:"view"`
}

func registerListServices(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "list_services",
		Description: "List services on a host or in a project. With host_id, read stored service records on that host, including the last known health status. With project_id, results are paged (next_cursor, prev_cursor, total) and limited to what that project may see. Optional q (at most 100 characters), sort (name, host, kind, status, last_seen), dir (asc or desc), limit (1–200), cursor, and view apply only with project_id. view is all (every row; omit view to leave the API default all), deploybot (source owned or declared, gone or not, except a gone owned row whose catalog entry was deleted, which only all lists), or deploybot_and_discovered (those rows plus discovered rows the host still reports). gone_at set means the host no longer reports the service (a stopped program or a listener that went away); null means the latest report still includes it. Results are limited to what the caller's projects may see. Does not contact the host.",
		Annotations: readOnlyAnnotations(false),
		InputSchema: json.RawMessage(listServicesSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in listServicesArgs
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		project, query, err := validateListServices(in)
		if err != nil {
			return badRequest(err)
		}
		var path string
		if project {
			path = idPath("v1", "projects", *in.ProjectID, "services")
		} else {
			path = idPath("v1", "hosts", *in.HostID, "services")
		}
		payload, fail := get(ctx, api, path, query)
		if fail != nil {
			return fail
		}
		return presentServices(payload, project)
	})
}

// presentServices lists only the items truncateLists keeps, then the
// truncation note on its own line. The head still reports the full count,
// or total in project mode.
func presentServices(payload json.RawMessage, project bool) *mcp.CallToolResult {
	shown, note := truncateLists(payload, maxListItems)
	summary := summarizeServices(payload, project)
	if note != "" {
		summary += "\n" + note
	}
	return present(shown, summary)
}

func validateListServices(in listServicesArgs) (bool, url.Values, error) {
	both := in.HostID != nil && in.ProjectID != nil
	neither := in.HostID == nil && in.ProjectID == nil
	if both || neither {
		return false, nil, errors.New("exactly one of host_id or project_id is required")
	}
	if in.HostID != nil {
		if err := requireUUID("host_id", *in.HostID); err != nil {
			return false, nil, err
		}
		if listServiceQuerySet(in) {
			return false, nil, errors.New("q, sort, dir, limit, cursor, and view are only allowed with project_id")
		}
		return false, nil, nil
	}
	if err := requireUUID("project_id", *in.ProjectID); err != nil {
		return false, nil, err
	}
	query, err := projectServiceQuery(in)
	if err != nil {
		return false, nil, err
	}
	return true, query, nil
}

func listServiceQuerySet(in listServicesArgs) bool {
	return in.Q != nil || in.Sort != nil || in.Dir != nil || in.Limit != nil || in.Cursor != nil || in.View != nil
}

func validServiceSort(s string) bool {
	switch s {
	case "name", "host", "kind", "status", "last_seen":
		return true
	default:
		return false
	}
}

func validServiceView(s string) bool {
	switch s {
	case "all", "deploybot", "deploybot_and_discovered":
		return true
	default:
		return false
	}
}

func projectServiceQuery(in listServicesArgs) (url.Values, error) {
	if in.Q != nil && utf8.RuneCountInString(*in.Q) > 100 {
		return nil, errors.New("q must be at most 100 characters")
	}
	if in.Sort != nil && !validServiceSort(*in.Sort) {
		return nil, errors.New("sort must be name, host, kind, status, or last_seen")
	}
	if in.Dir != nil && *in.Dir != "asc" && *in.Dir != "desc" {
		return nil, errors.New("dir must be asc or desc")
	}
	if in.View != nil && !validServiceView(*in.View) {
		return nil, errors.New("view must be all, deploybot, or deploybot_and_discovered")
	}
	if in.Limit != nil && (*in.Limit < 1 || *in.Limit > 200) {
		return nil, errors.New("limit must be between 1 and 200")
	}
	query := url.Values{}
	if in.Q != nil {
		query.Set("q", *in.Q)
	}
	if in.Sort != nil {
		query.Set("sort", *in.Sort)
	}
	if in.Dir != nil {
		query.Set("dir", *in.Dir)
	}
	if in.Limit != nil {
		query.Set("limit", strconv.Itoa(*in.Limit))
	}
	if in.Cursor != nil {
		query.Set("cursor", *in.Cursor)
	}
	if in.View != nil {
		query.Set("view", *in.View)
	}
	if len(query) == 0 {
		return nil, nil
	}
	return query, nil
}
