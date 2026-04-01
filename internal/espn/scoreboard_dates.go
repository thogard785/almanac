package espn

import (
	"context"
	"fmt"
	"time"
)

func fetchScoreboardEvents(ctx context.Context, client *Client, baseURL string) ([]scoreboardEvent, error) {
	dateSet := []string{
		time.Now().UTC().Format("20060102"),
		time.Now().UTC().Add(-24 * time.Hour).Format("20060102"),
	}

	seen := make(map[string]struct{}, 16)
	merged := make([]scoreboardEvent, 0, 16)
	for _, date := range dateSet {
		resp, err := FetchJSON[scoreboardResponse](ctx, client, fmt.Sprintf("%s?dates=%s", baseURL, date))
		if err != nil {
			return nil, err
		}
		for _, ev := range resp.Events {
			if _, ok := seen[ev.ID]; ok {
				continue
			}
			seen[ev.ID] = struct{}{}
			merged = append(merged, ev)
		}
	}
	return merged, nil
}
