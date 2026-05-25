package notification

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/0x626f/pgxext"
)

// Handler processes a received PostgreSQL notification.
type Handler func(context.Context, Event) error

// Consumer listens for PostgreSQL notifications on one channel.
type Consumer struct {
	ds      *pgxext.DataSource
	channel string
}

// NewConsumer returns a Consumer that listens on channel using ds.
func NewConsumer(ds *pgxext.DataSource, channel string) *Consumer {
	return &Consumer{ds: ds, channel: channel}
}

// Listen blocks until ctx is canceled, the LISTEN connection fails, or handler
// returns an error. It uses one acquired connection for the duration of the
// call because PostgreSQL LISTEN state is connection-local.
func (c *Consumer) Listen(ctx context.Context, handler Handler) error {
	return c.listen(ctx, nil, handler)
}

func (c *Consumer) listen(ctx context.Context, ready chan<- struct{}, handler Handler) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.ds == nil {
		return fmt.Errorf("notification: nil DataSource")
	}
	if handler == nil {
		return fmt.Errorf("notification: nil handler")
	}
	channel, err := quoteIdentifier(c.channel)
	if err != nil {
		return err
	}

	conn, err := c.ds.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		return err
	}
	if ready != nil {
		close(ready)
	}
	defer conn.Exec(context.Background(), "UNLISTEN "+channel) //nolint:errcheck

	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		event := Event{
			PID:     uint32(notification.PID),
			Channel: notification.Channel,
			Raw:     notification.Payload,
		}
		if notification.Payload != "" {
			if err := json.Unmarshal([]byte(notification.Payload), &event.Payload); err != nil {
				return fmt.Errorf("notification: decode payload: %w", err)
			}
		}
		if err := handler(ctx, event); err != nil {
			return err
		}
	}
}
