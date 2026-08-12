// Package wradapter attaches an oathgate widget to a session living in a
// windrunner daemon: the terminal outlives the app embedding it, several
// apps can watch one session, and Close merely detaches.
package wradapter

import (
	"context"
	"fmt"

	"github.com/trentkm/windrunner/client"

	"github.com/trentkm/oathgate"
)

// Attach opens a dedicated connection to one daemon session, sized to the
// widget's box before the snapshot is taken.
func Attach(c *client.Client, sessionID string, cols, rows int) (oathgate.Transport, error) {
	if err := c.Resize(sessionID, cols, rows); err != nil {
		return nil, fmt.Errorf("wradapter: %w", err)
	}
	attachment, err := c.Attach(sessionID, 256)
	if err != nil {
		return nil, fmt.Errorf("wradapter: %w", err)
	}
	return &transport{attachment: attachment}, nil
}

type transport struct {
	attachment *client.Attachment
}

func (t *transport) Seed() []byte          { return t.attachment.Snapshot().ANSI }
func (t *transport) Output() <-chan []byte { return t.attachment.Output() }
func (t *transport) Write(p []byte) error  { return t.attachment.Write(p) }
func (t *transport) Close()                { t.attachment.Close() }

func (t *transport) Resize(ctx context.Context, cols, rows int) error {
	return t.attachment.Resize(cols, rows)
}
