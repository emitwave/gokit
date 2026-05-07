// Package notifications provides a multi-channel notification dispatcher.
// One Notification can fan out to email, SMS, in-app database storage,
// Slack, push notifications — whatever channels you register.
//
// The shape:
//
//	type WelcomeEmail struct{ UserName string }
//
//	func (n WelcomeEmail) Channels() []string { return []string{"mail", "database"} }
//	func (n WelcomeEmail) Render(channel string) (notifications.Message, error) {
//	    switch channel {
//	    case "mail":
//	        return notifications.MailMessage{
//	            Subject: "Welcome!",
//	            Body:    "Hi " + n.UserName + ", thanks for joining.",
//	        }, nil
//	    case "database":
//	        return notifications.DatabaseMessage{
//	            Type: "welcome",
//	            Data: map[string]any{"name": n.UserName},
//	        }, nil
//	    }
//	    return nil, nil
//	}
//
//	notifier := notifications.New()
//	notifier.Register(notifications.NewLogChannel(logger))
//	notifier.Register(yourMailChannel)
//
//	notifier.Send(ctx, WelcomeEmail{UserName: "Tunde"}, user)
//
// Each registered channel decides how to deliver the rendered Message.
// Send returns an aggregated error if any channel fails, but does NOT
// short-circuit — every selected channel gets a chance to deliver.
//
// For async delivery, wrap Send in queue.Dispatch — the notification's
// payload is just the type name + Notification struct, both JSON-able.
package notifications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Notification is something a recipient gets. Channels() lists which
// transports to use; Render(channel) produces the channel-specific
// Message. Returning a nil Message skips that channel for this
// notification (useful when one notification only goes via some of its
// declared channels under certain conditions).
type Notification interface {
	Channels() []string
	Render(channel string) (Message, error)
}

// Notifiable is a recipient. RouteFor(channel) returns the channel-
// specific address: an email address for "mail", a phone number for
// "sms", a user ID for "database", a Slack channel ID for "slack", etc.
//
// Returning empty string is the convention for "no route on this
// channel" — channels skip the recipient when the route is empty.
type Notifiable interface {
	RouteFor(channel string) string
}

// Message is the rendered, channel-specific payload. Implementations are
// channel-specific structs (MailMessage, DatabaseMessage, etc.) — the
// channel asserts to its expected type.
type Message interface {
	// Kind names the message type so channels can validate the input
	// they received (e.g. MailChannel rejects DatabaseMessage).
	Kind() string
}

// ---------- built-in message types --------------------------------------

// MailMessage is the standard payload for "mail" channels.
type MailMessage struct {
	Subject string
	Body    string
	IsHTML  bool
}

func (MailMessage) Kind() string { return "mail" }

// SMSMessage is the standard payload for "sms" channels.
type SMSMessage struct {
	Text string
}

func (SMSMessage) Kind() string { return "sms" }

// DatabaseMessage is the standard payload for "database" channels (in-
// app notification storage).
type DatabaseMessage struct {
	Type string         // notification subtype (e.g. "user.followed")
	Data map[string]any // arbitrary structured payload
}

func (DatabaseMessage) Kind() string { return "database" }

// ---------- Channel interface -------------------------------------------

// Channel is a transport. The Notifier maintains a registry keyed by
// Name(); when sending, it calls Send on each channel listed in the
// notification's Channels().
//
// Implementations must be safe for concurrent calls — the Notifier may
// fan out across goroutines for async delivery.
type Channel interface {
	Name() string
	Send(ctx context.Context, msg Message, route string, recipient Notifiable) error
}

// ---------- Notifier (orchestrator) -------------------------------------

// Notifier registers channels and dispatches notifications across them.
type Notifier struct {
	channels map[string]Channel
}

// New returns a Notifier with no channels. Register what you need.
func New() *Notifier {
	return &Notifier{channels: make(map[string]Channel)}
}

// Register adds a channel. Re-registering the same name replaces the
// existing one — handy in tests where you want to swap a real channel
// for a fake.
func (n *Notifier) Register(c Channel) {
	n.channels[c.Name()] = c
}

// Channels returns the registered channel names. Useful in tests and
// for diagnostic endpoints.
func (n *Notifier) Channels() []string {
	out := make([]string, 0, len(n.channels))
	for k := range n.channels {
		out = append(out, k)
	}
	return out
}

// Send delivers a notification to one or more recipients across all of
// the notification's declared channels. Errors from individual channels
// are aggregated — every channel attempts delivery for every recipient
// even if earlier ones fail.
//
//	notifier.Send(ctx, WelcomeEmail{UserName: u.Name}, u)
//	notifier.Send(ctx, BulkAnnouncement{...}, recipients...)
func (n *Notifier) Send(ctx context.Context, notif Notification, recipients ...Notifiable) error {
	if len(recipients) == 0 {
		return nil
	}
	var errs []error
	for _, channelName := range notif.Channels() {
		ch, ok := n.channels[channelName]
		if !ok {
			errs = append(errs, fmt.Errorf("notifications: unregistered channel %q", channelName))
			continue
		}

		msg, err := notif.Render(channelName)
		if err != nil {
			errs = append(errs, fmt.Errorf("notifications: render %q: %w", channelName, err))
			continue
		}
		if msg == nil {
			// Skipped channel — notification opted out for this caller
			// or set of conditions.
			continue
		}

		for _, r := range recipients {
			route := r.RouteFor(channelName)
			if route == "" {
				continue
			}
			if err := ch.Send(ctx, msg, route, r); err != nil {
				errs = append(errs, fmt.Errorf("notifications: %s: %w", channelName, err))
			}
		}
	}
	return errors.Join(errs...)
}

// ---------- LogChannel (always available, useful for dev) ---------------

// LogChannel writes notifications to a slog.Logger instead of actually
// sending them. Always-on default for dev environments and a useful
// "tee" target alongside real channels in tests.
type LogChannel struct {
	name   string
	logger *slog.Logger
}

// NewLogChannel returns a channel that records to the given logger
// under the name "log". Use NewLogChannelAs to register multiple log
// channels (e.g. one shadowing "mail", one shadowing "sms") with
// distinct names.
func NewLogChannel(logger *slog.Logger) *LogChannel {
	return NewLogChannelAs("log", logger)
}

// NewLogChannelAs lets you register the log channel under any name —
// useful for shadowing "mail" or "sms" in dev so you can see what
// would have been sent without configuring real providers.
func NewLogChannelAs(name string, logger *slog.Logger) *LogChannel {
	return &LogChannel{name: name, logger: logger}
}

func (c *LogChannel) Name() string { return c.name }

func (c *LogChannel) Send(ctx context.Context, msg Message, route string, _ Notifiable) error {
	c.logger.Info("notification (logged, not sent)",
		"channel", c.name,
		"kind", msg.Kind(),
		"route", route,
		"message", fmt.Sprintf("%+v", msg),
	)
	return nil
}
