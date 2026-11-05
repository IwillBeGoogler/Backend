// Package emqx is the little of EMQX's REST API (v5) the API uses: kicking
// a client off the broker.
package emqx

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client calls the EMQX management API with an API key and secret (basic
// auth). The zero value, or one without URL or Key, is disabled.
type Client struct {
	URL, Key, Secret string
	HTTP             *http.Client
}

// Enabled reports whether an API URL and key are configured.
func (c *Client) Enabled() bool { return c != nil && c.URL != "" && c.Key != "" }

// Kick disconnects a client (DELETE /api/v5/clients/{clientid}). A client
// that is not connected is not an error.
func (c *Client) Kick(ctx context.Context, clientID string) error {
	if !c.Enabled() {
		return nil
	}
	u := strings.TrimRight(c.URL, "/") + "/api/v5/clients/" + url.PathEscape(clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.Key, c.Secret)
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 == 2 || res.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("emqx: kick %s: status %d", clientID, res.StatusCode)
}
