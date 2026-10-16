package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// Herdr answers one request per connection and then closes it, so every call
// dials afresh: there is no long-lived connection to lose.
type client struct {
	socket  string
	timeout time.Duration
}

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type response struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c client) call(method string, params, out any) error {
	conn, err := net.DialTimeout("unix", c.socket, c.timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return err
	}

	if params == nil {
		params = struct{}{} // Herdr rejects a request without params.
	}
	req, err := json.Marshal(request{ID: method, Method: method, Params: params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return err
	}
	var resp response
	if err := json.Unmarshal(line, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("%s: %s: %s", method, resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil {
		return errors.New(method + ": empty result")
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}

// Only the fields this plugin reads.
type snapshot struct {
	Tabs    []tab    `json:"tabs"`
	Panes   []pane   `json:"panes"`
	Layouts []layout `json:"layouts"`
}

type tab struct {
	ID    string `json:"tab_id"`
	Label string `json:"label"`
}

type pane struct {
	ID            string  `json:"pane_id"`
	TabID         string  `json:"tab_id"`
	Cwd           *string `json:"cwd"`
	ForegroundCwd *string `json:"foreground_cwd"`
}

type layout struct {
	TabID         string `json:"tab_id"`
	FocusedPaneID string `json:"focused_pane_id"`
}

func (c client) snapshot() (snapshot, error) {
	var res struct {
		Snapshot snapshot `json:"snapshot"`
	}
	err := c.call("session.snapshot", nil, &res)
	return res.Snapshot, err
}

func (c client) renameTab(tabID, label string) error {
	return c.call("tab.rename", map[string]string{"tab_id": tabID, "label": label}, nil)
}
