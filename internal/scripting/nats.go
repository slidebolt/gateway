package scripting

import (
	"github.com/nats-io/nats.go"
)

// NATSBus adapts a *nats.Conn to the EventBus interface.
type NATSBus struct {
	nc *nats.Conn
}

// NewNATSBus wraps a NATS connection as an EventBus.
func NewNATSBus(nc *nats.Conn) EventBus {
	return &NATSBus{nc: nc}
}

func (b *NATSBus) Publish(subject string, data []byte) error {
	return b.nc.Publish(subject, data)
}

func (b *NATSBus) Subscribe(subject string, handler func([]byte)) (Subscription, error) {
	sub, err := b.nc.Subscribe(subject, func(m *nats.Msg) {
		handler(m.Data)
	})
	if err != nil {
		return nil, err
	}
	return &natsSub{sub: sub}, nil
}

type natsSub struct{ sub *nats.Subscription }

func (s *natsSub) Unsubscribe() error { return s.sub.Unsubscribe() }
