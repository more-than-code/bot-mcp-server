package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

// An image deploy or rollback confirm waits at most apiclient.MaxCallTimeout
// (50s), then reports "still running on the host". The API keeps the action
// running after the call ends (a context without cancel), so a long pull or
// health wait finishes on the host and entry_status shows the result. MCP
// clients cut tool calls long before a 990s pull + health budget would end.
// A known program keeps the default client timeout.
// confirmWait is the deadline for POST /v1/confirmations/{id}.
// deploy is false for a known program: use the default timeout and the
// generic API error. deploy is true when this confirm runs an image action.
type confirmWait struct {
	limit  time.Duration
	deploy bool
}

// confirmWaitFor reads the catalog with the default deadline.
// A failed lookup is an unknown budget (MaxCallTimeout), not a tool error.
func confirmWaitFor(ctx context.Context, api *apiclient.Client, projectID, entry string) confirmWait {
	path := idPath("v1", "projects", projectID, "catalog")
	payload, fail := get(ctx, api, path, nil)
	if fail != nil {
		return confirmWait{limit: apiclient.MaxCallTimeout, deploy: true}
	}
	return waitFromCatalog(payload, entry)
}

func waitFromCatalog(raw json.RawMessage, entry string) confirmWait {
	var body struct {
		Items []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return confirmWait{limit: apiclient.MaxCallTimeout, deploy: true}
	}
	for _, item := range body.Items {
		if item.Name == entry && item.Kind != "image" {
			return confirmWait{limit: apiclient.Timeout, deploy: false}
		}
	}
	return confirmWait{limit: apiclient.MaxCallTimeout, deploy: true}
}
