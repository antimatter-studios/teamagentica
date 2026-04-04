package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// DeployCandidate tells the kernel to start a candidate container for the given plugin.
func (c *Client) DeployCandidate(ctx context.Context, pluginID string, image string) error {
	payload := map[string]interface{}{"image": image}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/plugins/deploy/%s", c.kernelURL(), pluginID),
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("deploy returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// PromoteCandidate tells the kernel to promote a candidate to primary.
func (c *Client) PromoteCandidate(ctx context.Context, pluginID string) error {
	return c.pluginAction(ctx, pluginID, "promote")
}

// RollbackCandidate tells the kernel to stop a candidate and revert to primary.
func (c *Client) RollbackCandidate(ctx context.Context, pluginID string) error {
	return c.pluginAction(ctx, pluginID, "rollback")
}

func (c *Client) pluginAction(ctx context.Context, pluginID, action string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/plugins/%s/%s", c.kernelURL(), action, pluginID),
		bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s returned %d: %s", action, resp.StatusCode, string(respBody))
	}
	return nil
}
