package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

// Deploy waits are the budgets for the confirm call that runs the action.
// A service uses the maximum: 300s pull + 600s healthy_after + 90s slack.
// A stored service spec includes timeout_sec 0; that unused field is ignored.
// A job uses 300s + timeout_sec + 90s. A stored job spec includes
// healthy_after_sec 0; that unused field is ignored.
// An unknown entry uses MaxCallTimeout.
// A known program keeps the default client timeout.
const (
	deployPullBudget    = 300 * time.Second
	deployHealthMax     = 600 * time.Second
	deploySlack         = 90 * time.Second
	deployServiceBudget = deployPullBudget + deployHealthMax + deploySlack
	defaultJobTimeout   = 600
	minJobTimeout       = 10
	maxJobTimeout       = 3600
)

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
			Spec struct {
				Kind       string `json:"kind"`
				TimeoutSec *int   `json:"timeout_sec"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return confirmWait{limit: apiclient.MaxCallTimeout, deploy: true}
	}
	for _, item := range body.Items {
		if item.Name != entry {
			continue
		}
		if item.Kind != "image" {
			return confirmWait{limit: apiclient.Timeout, deploy: false}
		}
		switch item.Spec.Kind {
		case "service":
			return confirmWait{limit: deployServiceBudget, deploy: true}
		case "job":
			return confirmWait{limit: jobBudget(item.Spec.TimeoutSec), deploy: true}
		default:
			return confirmWait{limit: apiclient.MaxCallTimeout, deploy: true}
		}
	}
	return confirmWait{limit: apiclient.MaxCallTimeout, deploy: true}
}

func jobBudget(timeoutSec *int) time.Duration {
	sec := defaultJobTimeout
	if timeoutSec != nil {
		sec = *timeoutSec
	}
	if sec < minJobTimeout || sec > maxJobTimeout {
		return apiclient.MaxCallTimeout
	}
	return deployPullBudget + time.Duration(sec)*time.Second + deploySlack
}
