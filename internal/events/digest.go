package events

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// digestPayload is what the worker's rate valve builds: the event kinds that
// overflowed one chat's per-minute budget.
type digestPayload struct {
	Items []string `json:"items"`
}

func init() {
	// Not a GitHub kind: rows are produced locally by the outbox valve.
	Register("digest", nil, renderDigest)
}

func renderDigest(raw json.RawMessage) (string, error) {
	var p digestPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse digest: %w", err)
	}

	counts := map[string]int{}
	for _, kind := range p.Items {
		counts[kind]++
	}
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)

	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		if counts[kind] > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", kind, counts[kind]))
		} else {
			parts = append(parts, kind)
		}
	}

	return fmt.Sprintf("📋 Слишком много событий за минуту — ещё %d: %s",
		len(p.Items), strings.Join(parts, ", ")), nil
}
