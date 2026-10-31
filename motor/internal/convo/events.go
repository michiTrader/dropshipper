package convo

import "sync"

// Event tells open panels that something changed (they refetch what they show).
type Event struct {
	Type  string `json:"type"` // mensaje, escribiendo, listo, pedido, alerta, contacto, whatsapp
	Phone string `json:"phone,omitempty"`
	ID    int64  `json:"id,omitempty"`
	Text  string `json:"text,omitempty"`
}

// Broker fans events out to subscribers (Server-Sent Events in the API).
type Broker struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// NewBroker returns an empty broker.
func NewBroker() *Broker { return &Broker{subs: map[chan Event]struct{}{}} }

// Subscribe returns a channel of events and a function to stop.
func (b *Broker) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

// Publish sends an event to every subscriber; slow subscribers drop events
// rather than blocking the conversation.
func (b *Broker) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}
