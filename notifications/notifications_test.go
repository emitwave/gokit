package notifications

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// fakeChannel collects what was sent so we can assert on it.
type fakeChannel struct {
	mu       sync.Mutex
	name     string
	failNext error
	sent     []sentItem
}

type sentItem struct {
	msg   Message
	route string
}

func (f *fakeChannel) Name() string { return f.name }
func (f *fakeChannel) Send(_ context.Context, msg Message, route string, _ Notifiable) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return err
	}
	f.sent = append(f.sent, sentItem{msg, route})
	return nil
}

// fakeUser implements Notifiable.
type fakeUser struct {
	email, phone, id string
}

func (u fakeUser) RouteFor(channel string) string {
	switch channel {
	case "mail":
		return u.email
	case "sms":
		return u.phone
	case "database":
		return u.id
	}
	return ""
}

// welcomeNotif implements Notification.
type welcomeNotif struct {
	Name string
}

func (welcomeNotif) Channels() []string { return []string{"mail", "database"} }

func (n welcomeNotif) Render(channel string) (Message, error) {
	switch channel {
	case "mail":
		return MailMessage{Subject: "Welcome!", Body: "Hi " + n.Name}, nil
	case "database":
		return DatabaseMessage{Type: "welcome", Data: map[string]any{"name": n.Name}}, nil
	}
	return nil, nil
}

func TestSendDispatchesAcrossChannels(t *testing.T) {
	mail := &fakeChannel{name: "mail"}
	db := &fakeChannel{name: "database"}

	n := New()
	n.Register(mail)
	n.Register(db)

	user := fakeUser{email: "a@b.com", id: "u-1"}
	if err := n.Send(context.Background(), welcomeNotif{Name: "Tunde"}, user); err != nil {
		t.Fatal(err)
	}

	if len(mail.sent) != 1 {
		t.Errorf("mail: got %d sent, want 1", len(mail.sent))
	}
	if len(db.sent) != 1 {
		t.Errorf("database: got %d sent, want 1", len(db.sent))
	}
	if got := mail.sent[0].msg.(MailMessage).Subject; got != "Welcome!" {
		t.Errorf("mail subject: got %q", got)
	}
	if got := mail.sent[0].route; got != "a@b.com" {
		t.Errorf("mail route: got %q", got)
	}
}

func TestSendSkipsChannelsWithoutRoute(t *testing.T) {
	// User has email but no SMS phone — sms channel should not be called.
	mail := &fakeChannel{name: "mail"}
	sms := &fakeChannel{name: "sms"}

	n := New()
	n.Register(mail)
	n.Register(sms)

	type bothChan struct{ Body string }
	type bothNotif struct{ Body string }
	// Inline notification covering both channels:
	notif := bothChannelNotif{Body: "ping"}

	user := fakeUser{email: "x@y.z"} // no phone
	if err := n.Send(context.Background(), notif, user); err != nil {
		t.Fatal(err)
	}

	if len(mail.sent) != 1 {
		t.Errorf("mail should be sent (route present): got %d", len(mail.sent))
	}
	if len(sms.sent) != 0 {
		t.Errorf("sms should be skipped (empty route): got %d sent", len(sms.sent))
	}
}

type bothChannelNotif struct{ Body string }

func (bothChannelNotif) Channels() []string { return []string{"mail", "sms"} }
func (n bothChannelNotif) Render(ch string) (Message, error) {
	switch ch {
	case "mail":
		return MailMessage{Subject: "Both", Body: n.Body}, nil
	case "sms":
		return SMSMessage{Text: n.Body}, nil
	}
	return nil, nil
}

func TestSendUnregisteredChannelSurfacesError(t *testing.T) {
	n := New()
	// register only "mail" — the notification also asks for "database"
	mail := &fakeChannel{name: "mail"}
	n.Register(mail)

	err := n.Send(context.Background(), welcomeNotif{Name: "x"}, fakeUser{email: "a@b.c"})
	if err == nil {
		t.Fatal("expected error for unregistered channel")
	}
	if !strings.Contains(err.Error(), "database") {
		t.Errorf("error should name the missing channel: %v", err)
	}
	// Mail still went through — errors don't short-circuit.
	if len(mail.sent) != 1 {
		t.Errorf("mail should have sent despite database missing: got %d", len(mail.sent))
	}
}

func TestSendChannelFailureDoesntStopOthers(t *testing.T) {
	mail := &fakeChannel{name: "mail", failNext: errors.New("smtp down")}
	db := &fakeChannel{name: "database"}

	n := New()
	n.Register(mail)
	n.Register(db)

	err := n.Send(context.Background(), welcomeNotif{Name: "x"}, fakeUser{email: "a@b.c", id: "u1"})
	if err == nil {
		t.Fatal("expected error from mail channel")
	}
	if len(db.sent) != 1 {
		t.Error("database send should have happened despite mail failure")
	}
}

func TestSendToMultipleRecipients(t *testing.T) {
	mail := &fakeChannel{name: "mail"}
	n := New()
	n.Register(mail)

	users := []Notifiable{
		fakeUser{email: "a@x"},
		fakeUser{email: "b@x"},
		fakeUser{email: "c@x"},
	}
	if err := n.Send(context.Background(), mailOnlyNotif{}, users...); err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 3 {
		t.Errorf("expected 3 sends, got %d", len(mail.sent))
	}
}

type mailOnlyNotif struct{}

func (mailOnlyNotif) Channels() []string                  { return []string{"mail"} }
func (mailOnlyNotif) Render(string) (Message, error)     {
	return MailMessage{Subject: "Hi", Body: "broadcast"}, nil
}

func TestRenderNilSkipsChannel(t *testing.T) {
	// A notification can opt out of a channel by returning (nil, nil).
	mail := &fakeChannel{name: "mail"}
	n := New()
	n.Register(mail)

	if err := n.Send(context.Background(), conditionalNotif{}, fakeUser{email: "a@b.c"}); err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 0 {
		t.Errorf("nil-rendered channel should be skipped: got %d sent", len(mail.sent))
	}
}

type conditionalNotif struct{}

func (conditionalNotif) Channels() []string                       { return []string{"mail"} }
func (conditionalNotif) Render(string) (Message, error)           { return nil, nil }

func TestLogChannel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	ch := NewLogChannel(logger)

	n := New()
	n.Register(NewLogChannelAs("mail", logger)) // log shadowing "mail"

	if err := n.Send(context.Background(), welcomeNotif{Name: "Tunde"}, fakeUser{email: "x@y", id: "u1"}); err != nil {
		// "database" channel won't be registered — expect that error.
		if !strings.Contains(err.Error(), "database") {
			t.Fatal(err)
		}
	}

	if !strings.Contains(buf.String(), "Welcome") {
		t.Errorf("expected log to contain rendered subject; got %q", buf.String())
	}

	_ = ch // silence unused
}
