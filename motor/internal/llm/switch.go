package llm

import (
	"context"
	"sync"
)

// Switch is a Completer whose backing client can be swapped at runtime, so
// changing the AI provider from the panel or the terminal needs no restart.
type Switch struct {
	mu    sync.RWMutex
	c     *Client
	model string
	temp  *float64
}

// Set replaces the active client (nil disables the AI).
func (s *Switch) Set(c *Client, model string, temp *float64) {
	s.mu.Lock()
	s.c, s.model, s.temp = c, model, temp
	s.mu.Unlock()
}

// Ready reports whether a usable provider is active.
func (s *Switch) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.c != nil && (s.c.APIKey != "" || s.c.AllowNoKey)
}

// Model is the active model name.
func (s *Switch) Model() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.model
}

// Complete forwards to the active client, filling model/temperature defaults.
func (s *Switch) Complete(ctx context.Context, req Request) (Response, error) {
	s.mu.RLock()
	c, model, temp := s.c, s.model, s.temp
	s.mu.RUnlock()
	if c == nil {
		return Response{}, ErrNoAPIKey
	}
	if req.Model == "" {
		req.Model = model
	}
	if req.Temperature == nil {
		req.Temperature = temp
	}
	return c.Complete(ctx, req)
}
