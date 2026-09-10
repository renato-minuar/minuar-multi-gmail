package gmail

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// systemLabelIDs pass through resolution unchanged.
var systemLabelIDs = map[string]bool{
	"INBOX": true, "UNREAD": true, "STARRED": true, "IMPORTANT": true, "SPAM": true, "TRASH": true,
	"SENT": true, "DRAFT": true, "CHAT": true,
	"CATEGORY_PERSONAL": true, "CATEGORY_SOCIAL": true, "CATEGORY_PROMOTIONS": true,
	"CATEGORY_UPDATES": true, "CATEGORY_FORUMS": true,
}

type labelCache struct {
	mu     sync.Mutex
	loaded bool
	labels []Label
	byID   map[string]string
	byName map[string]string
}

// loadLabels fills the cache. force reloads even when already loaded.
func (c *Client) loadLabels(ctx context.Context, force bool) error {
	c.labels.mu.Lock()
	defer c.labels.mu.Unlock()
	if c.labels.loaded && !force {
		return nil
	}
	var labels []Label
	err := c.do(ctx, func(ctx context.Context) error {
		resp, err := c.svc.Users.Labels.List("me").Context(ctx).Do()
		if err != nil {
			return err
		}
		labels = labels[:0]
		for _, l := range resp.Labels {
			labels = append(labels, Label{ID: l.Id, Name: l.Name, Type: l.Type})
		}
		return nil
	})
	if err != nil {
		return c.mapErr(err, "")
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i].Name < labels[j].Name })
	c.labels.labels = labels
	c.labels.byID = make(map[string]string, len(labels))
	c.labels.byName = make(map[string]string, len(labels))
	for _, l := range labels {
		c.labels.byID[l.ID] = l.Name
		c.labels.byName[l.Name] = l.ID
	}
	c.labels.loaded = true
	return nil
}

// labelNames maps IDs to names. Unknown IDs are returned as-is so a label
// created after the cache loaded never breaks rendering.
func (c *Client) labelNames(ids []string) []string {
	c.labels.mu.Lock()
	defer c.labels.mu.Unlock()
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if name, ok := c.labels.byID[id]; ok {
			out = append(out, name)
		} else {
			out = append(out, id)
		}
	}
	return out
}

func (c *Client) lookupLabel(value string) (string, bool) {
	c.labels.mu.Lock()
	defer c.labels.mu.Unlock()
	if id, ok := c.labels.byName[value]; ok {
		return id, true
	}
	if _, ok := c.labels.byID[value]; ok {
		return value, true
	}
	return "", false
}

func (c *Client) labelNameList() string {
	c.labels.mu.Lock()
	defer c.labels.mu.Unlock()
	names := make([]string, 0, len(c.labels.labels))
	for _, l := range c.labels.labels {
		names = append(names, l.Name)
	}
	return strings.Join(names, ", ")
}

// resolveLabelIDs accepts system IDs, label names (exact, case-sensitive)
// or label IDs. One cache refresh is allowed on a miss. Labels are never
// created here. System IDs pass through without ever touching the cache,
// so a call made only of system IDs (e.g. archive, mark read) never
// forces a labels.list round trip on a cold cache.
func (c *Client) resolveLabelIDs(ctx context.Context, values []string) ([]string, error) {
	trimmed := make([]string, 0, len(values))
	needsCache := false
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		trimmed = append(trimmed, v)
		if !systemLabelIDs[v] {
			needsCache = true
		}
	}
	if needsCache {
		if err := c.loadLabels(ctx, false); err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(trimmed))
	refreshed := false
	for _, v := range trimmed {
		if systemLabelIDs[v] {
			out = append(out, v)
			continue
		}
		id, ok := c.lookupLabel(v)
		if !ok && !refreshed {
			if err := c.loadLabels(ctx, true); err != nil {
				return nil, err
			}
			refreshed = true
			id, ok = c.lookupLabel(v)
		}
		if !ok {
			return nil, fmt.Errorf("unknown label %q (available: %s)", v, c.labelNameList())
		}
		out = append(out, id)
	}
	return out, nil
}
