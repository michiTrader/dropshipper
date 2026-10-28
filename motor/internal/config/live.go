package config

import "sync"

// Live holds the business configuration that can change while the Motor runs
// (edited from the panel or the terminal). Readers always call Get.
type Live struct {
	mu sync.RWMutex
	n  Negocio
}

// NewLive wraps an initial configuration.
func NewLive(n Negocio) *Live { return &Live{n: n} }

// Get returns a copy of the current configuration.
func (l *Live) Get() Negocio {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.n
}

// Set replaces the configuration.
func (l *Live) Set(n Negocio) {
	l.mu.Lock()
	l.n = n
	l.mu.Unlock()
}
