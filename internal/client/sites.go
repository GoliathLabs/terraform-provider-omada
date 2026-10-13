package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Site is a controller site as returned by the sites list endpoint.
type Site struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListSites returns every site visible to the configured controller account.
func (c *Client) ListSites(ctx context.Context) ([]Site, error) {
	return listAllPages[Site](ctx, c, c.classicPath("sites"))
}

// ResolveSite resolves a site name or ID to its ID. An exact ID match wins;
// otherwise sites are matched by name, case-insensitively.
func (c *Client) ResolveSite(ctx context.Context, nameOrID string) (string, error) {
	sites, err := c.ListSites(ctx)
	if err != nil {
		return "", fmt.Errorf("listing sites: %w", err)
	}

	for _, s := range sites {
		if s.ID == nameOrID {
			return s.ID, nil
		}
	}
	for _, s := range sites {
		if strings.EqualFold(s.Name, nameOrID) {
			return s.ID, nil
		}
	}

	return "", fmt.Errorf("no site found with name or ID %q", nameOrID)
}

// FindSite finds a site by its ID. A nil, nil return means the configured
// controller account sees no site with that ID.
func (c *Client) FindSite(ctx context.Context, id string) (*Site, error) {
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	for _, site := range sites {
		if site.ID == id {
			return &site, nil
		}
	}
	return nil, nil
}

// SiteDetail is a site as returned by the Open API's site endpoint. Raw is
// retained because the only way to change a site is a full-object PUT:
// sending back what was read keeps the region, time zone, NTP, DST, scenario
// and location settings this provider does not model.
type SiteDetail struct {
	Name string
	Raw  map[string]any
}

// UnmarshalJSON decodes the modelled fields while retaining the complete site.
func (s *SiteDetail) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Raw = raw
	s.Name, _ = raw["name"].(string)
	return nil
}

// sitePath builds the Open API path of the site itself.
func (c *Client) sitePath(siteID string) string {
	return strings.TrimSuffix(c.openAPIPath("v1", siteID, ""), "/")
}

// GetSite reads the full site document. Unlike FindSite it fails for a site
// that does not exist: the Open API answers both a missing site and one the
// credentials may not access with the same permission error.
func (c *Client) GetSite(ctx context.Context, siteID string) (*SiteDetail, error) {
	var site SiteDetail
	if err := c.doPublicOpenAPI(ctx, "GET", c.sitePath(siteID), nil, &site); err != nil {
		return nil, err
	}
	if site.Raw == nil {
		return nil, fmt.Errorf("the controller returned no settings for site %q", siteID)
	}
	return &site, nil
}

// RenameSite changes a site's name and nothing else. The endpoint is a
// full-object PUT with mandatory region, scenario and time zone, so the site
// is read first and written back with only the name replaced.
func (c *Client) RenameSite(ctx context.Context, siteID, name string) error {
	current, err := c.GetSite(ctx, siteID)
	if err != nil {
		return err
	}
	return c.doPublicOpenAPI(ctx, "PUT", c.sitePath(siteID), siteUpdateBody(current.Raw, name), nil)
}

// siteUpdateBody turns a site as read into the body of its update request.
// The identity fields siteId and type are not part of the update schema, and
// dst is cut down to the fields that schema knows: the read side adds computed
// timestamps and, while DST is not configured manually, empty start and end
// objects whose members the write side declares mandatory. Every other key,
// including ones this client has never seen, is passed through unchanged, so
// that a field added by a newer controller is preserved rather than reset.
func siteUpdateBody(current map[string]any, name string) map[string]any {
	body := make(map[string]any, len(current))
	for key, value := range current {
		switch key {
		case "siteId", "type":
		case "dst":
			body[key] = siteDSTUpdate(value)
		default:
			body[key] = value
		}
	}
	body["name"] = name
	return body
}

// siteDSTManual is the dst.mode for a manually configured DST period, the
// only mode in which the start, end and offset fields carry configuration.
const siteDSTManual = 2

func siteDSTUpdate(value any) any {
	dst, ok := value.(map[string]any)
	if !ok {
		return value
	}
	keys := []string{"enable", "mode"}
	if int64Value(dst["mode"]) == siteDSTManual {
		keys = append(keys, "start", "end", "offset")
	}
	update := make(map[string]any, len(keys))
	for _, key := range keys {
		if field, ok := dst[key]; ok {
			update[key] = field
		}
	}
	return update
}
