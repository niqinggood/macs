package macs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxSSELineBytes = 1 << 20

type callbackError struct{ err error }

func (err *callbackError) Error() string { return err.err.Error() }
func (err *callbackError) Unwrap() error { return err.err }

// WatchOptions configures one task event subscription. LastEventID is sent to
// servers that support bounded replay; older MACS servers safely ignore it.
type WatchOptions struct {
	ClientID    string
	LastEventID string
}

// WatchTask consumes the low-latency AgentOS event stream until the server
// closes it, the context is cancelled, or handler returns an error. The stream
// is not a durable event log; callers needing a final result should use WaitTask.
func (client *Client) WatchTask(ctx context.Context, taskID, clientID string, handler func(Event) error) error {
	return client.WatchTaskWithOptions(ctx, taskID, WatchOptions{ClientID: clientID}, handler)
}

// WatchTaskWithOptions consumes one low-latency AgentOS event stream.
func (client *Client) WatchTaskWithOptions(ctx context.Context, taskID string, options WatchOptions, handler func(Event) error) error {
	if handler == nil {
		return errors.New("MACS SDK: event handler is required")
	}
	path, err := taskPath(taskID, "events")
	if err != nil {
		return err
	}
	if clientID := strings.TrimSpace(options.ClientID); clientID != "" {
		path += "?client_id=" + url.QueryEscape(clientID)
	}
	request, err := client.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Cache-Control", "no-cache")
	if lastEventID := strings.TrimSpace(options.LastEventID); lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("MACS SDK: open task event stream: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeAPIError(response)
	}
	mediaType, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if parseErr != nil || !strings.EqualFold(mediaType, "text/event-stream") {
		return fmt.Errorf("MACS SDK: task event endpoint returned content type %q", response.Header.Get("Content-Type"))
	}
	if err := consumeSSE(response.Body, handler); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	return nil
}

// WaitOptions configures WaitTask.
type WaitOptions struct {
	PollInterval  time.Duration
	ClientID      string
	OnEvent       func(Event) error
	OnProgress    func(Progress) error
	OnSnapshot    func(Task) error
	DisableEvents bool
}

// WaitTask waits for a durable terminal task state. By default it uses SSE for
// low latency, then queries the task endpoint after the stream closes. If the
// stream is unavailable or disconnected, it falls back to bounded polling.
func (client *Client) WaitTask(ctx context.Context, taskID string, options WaitOptions) (*Task, error) {
	interval := options.PollInterval
	if interval <= 0 {
		interval = time.Second
	}

	current, err := client.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if options.OnSnapshot != nil {
		if err := options.OnSnapshot(*current); err != nil {
			return nil, err
		}
	}
	if current.Terminal() {
		return current, nil
	}

	if !options.DisableEvents {
		handler := func(event Event) error {
			if options.OnEvent != nil {
				if err := options.OnEvent(event); err != nil {
					return err
				}
			}
			if options.OnProgress != nil {
				progress, matched, err := event.DecodeProgress()
				if err != nil {
					return err
				}
				if matched {
					if err := options.OnProgress(progress); err != nil {
						return err
					}
				}
			}
			if options.OnSnapshot != nil {
				snapshot, matched, err := event.DecodeTaskSnapshot()
				if err != nil {
					return err
				}
				if matched {
					if err := options.OnSnapshot(snapshot); err != nil {
						return err
					}
				}
			}
			return nil
		}
		streamErr := client.WatchTask(ctx, taskID, options.ClientID, handler)
		if streamErr != nil {
			var callbackErr *callbackError
			if errors.As(streamErr, &callbackErr) {
				return nil, callbackErr.err
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
		}
	}

	for {
		current, err = client.GetTask(ctx, taskID)
		if err != nil {
			return nil, err
		}
		if options.OnSnapshot != nil {
			if err := options.OnSnapshot(*current); err != nil {
				return nil, err
			}
		}
		if current.Terminal() {
			return current, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func consumeSSE(reader io.Reader, handler func(Event) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), maxSSELineBytes)
	event := Event{Type: "message"}
	var data bytes.Buffer
	dispatch := func() error {
		if data.Len() == 0 {
			event = Event{Type: "message"}
			return nil
		}
		payload := bytes.TrimSuffix(data.Bytes(), []byte("\n"))
		event.Data = append(event.Data[:0], payload...)
		if err := handler(event); err != nil {
			return &callbackError{err: err}
		}
		event = Event{Type: "message"}
		data.Reset()
		return nil
	}

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if found && strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "event":
			if value != "" {
				event.Type = value
			}
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
		case "id":
			if !strings.ContainsRune(value, '\x00') {
				event.ID = value
			}
		case "retry":
			if milliseconds, err := strconv.ParseInt(value, 10, 64); err == nil && milliseconds >= 0 {
				event.Retry = time.Duration(milliseconds) * time.Millisecond
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("MACS SDK: read task event stream: %w", err)
	}
	return dispatch()
}
