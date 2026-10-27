package box

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// HelperError is a box's refusal on one of its helper routes, with the code
// to branch on.
type HelperError struct {
	Status  int
	Code    string
	Message string
	// NoHelpers: the box's berthd does not run helpers at all: it is older
	// than they are, or runs somewhere they cannot.
	NoHelpers bool
}

func (e *HelperError) Error() string { return e.Message }

// helperCall makes one request of a box's helper routes and decodes its
// JSON answer into out, or returns a *HelperError for a refusal.
func (c *Client) helperCall(ctx context.Context, method, path string, header http.Header, body io.Reader, out any) error {
	if header == nil {
		header = http.Header{}
	}
	if validOrigin.MatchString(c.Origin) {
		header.Set(OriginHeader, c.Origin)
	}
	resp, err := c.Doer.DoWithHeader(ctx, method, path, body, header)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		he := &HelperError{Status: resp.StatusCode, Message: "box replied " + resp.Status}
		var e struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e) == nil && e.Error != "" {
			he.Message, he.Code = e.Error, e.Code
		}
		// A route the box does not know is answered by Go's own mux in
		// plain text: its berthd is older than helpers.
		unknown := (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) &&
			strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain")
		if unknown {
			he.Message = "this box runs an older berthd, which cannot run helpers; update it"
		}
		he.NoHelpers = unknown || resp.StatusCode == http.StatusNotImplemented
		return he
	}
	if raw, ok := out.(*[]byte); ok {
		*raw, err = io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		return err
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Helpers lists the helpers of plugins on the box, and its platform.
func (c *Client) Helpers(ctx context.Context) (out HelperList, err error) {
	return out, c.helperCall(ctx, http.MethodGet, "/v1/helpers", nil, nil, &out)
}

// Helper is one plugin's helper on the box; its state is off when the box
// has none for that plugin.
func (c *Client) Helper(ctx context.Context, plugin string) (out HelperStatus, err error) {
	return out, c.helperCall(ctx, http.MethodGet, "/v1/helpers/"+url.PathEscape(plugin), nil, nil, &out)
}

// InstallHelper uploads a helper's program and has the box run it. With no
// program the box starts the one of that checksum it already holds.
func (c *Client) InstallHelper(ctx context.Context, plugin string, spec HelperSpec, program []byte) (out HelperStatus, err error) {
	header := http.Header{HelperSpecHeader: {EncodeHelperSpec(spec)}, "Content-Type": {"application/octet-stream"}}
	var body io.Reader
	if len(program) > 0 {
		body = bytes.NewReader(program)
	}
	return out, c.helperCall(ctx, http.MethodPut, "/v1/helpers/"+url.PathEscape(plugin), header, body, &out)
}

// RemoveHelper stops a plugin's helper and removes it; purge removes its
// data too.
func (c *Client) RemoveHelper(ctx context.Context, plugin string, purge bool) (out HelperStatus, err error) {
	path := "/v1/helpers/" + url.PathEscape(plugin)
	if purge {
		path += "?purge=1"
	}
	return out, c.helperCall(ctx, http.MethodDelete, path, nil, nil, &out)
}

// AttachHelperEnv has the box tell a plugin's helper what a worktree's .env
// files have of the variables its manifest names (helperenv.go). worktree
// "" is the location's main checkout.
func (c *Client) AttachHelperEnv(ctx context.Context, plugin, location, worktree string) (out HelperEnvLook, err error) {
	body, err := json.Marshal(map[string]string{"location": location, "worktree": worktree})
	if err != nil {
		return out, err
	}
	header := http.Header{"Content-Type": {"application/json"}}
	return out, c.helperCall(ctx, http.MethodPost, "/v1/helpers/"+url.PathEscape(plugin)+"/attach-env", header, bytes.NewReader(body), &out)
}

// AttachHelperGiven has the box tell a plugin's helper the variables a
// person gave for a worktree, in place of what was given before; with none,
// that is taken back. No file is read.
func (c *Client) AttachHelperGiven(ctx context.Context, plugin, location, worktree string, env map[string]string) (out HelperEnvLook, err error) {
	if env == nil {
		env = map[string]string{}
	}
	body, err := json.Marshal(map[string]any{"location": location, "worktree": worktree, "env": env})
	if err != nil {
		return out, err
	}
	header := http.Header{"Content-Type": {"application/json"}}
	return out, c.helperCall(ctx, http.MethodPost, "/v1/helpers/"+url.PathEscape(plugin)+"/attach-env", header, bytes.NewReader(body), &out)
}

// HelperLog is the end of what a helper printed.
func (c *Client) HelperLog(ctx context.Context, plugin string, limit int64) (out []byte, err error) {
	return out, c.helperCall(ctx, http.MethodGet, fmt.Sprintf("/v1/helpers/%s/log?limit=%d", url.PathEscape(plugin), limit), nil, nil, &out)
}
