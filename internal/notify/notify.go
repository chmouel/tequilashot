package notify

import (
	"context"
	"errors"
	"fmt"
	"html"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	sendTimeout = 5 * time.Second
	service     = "org.freedesktop.Notifications"
	objectPath  = dbus.ObjectPath("/org/freedesktop/Notifications")
	desktopID   = "com.chmouel.Tequilashot.Recorder"
)

// Action buttons appear in slice order when supported by the notification service
type Action struct {
	ID    string
	Label string
}

// Send uses milliseconds for timeout, with -1 selecting the desktop's default
func Send(timeout int, icon, title, body string) error {
	_, err := send(context.Background(), timeout, icon, title, body, nil)
	return err
}

// SendWithActions returns an empty action on dismissal or expiry.
// Non-positive timeouts limit the action wait to 30 seconds.
func SendWithActions(timeout int, icon, title, body string, actions []Action) (string, error) {
	return send(context.Background(), timeout, icon, title, body, actions)
}

func send(ctx context.Context, timeout int, icon, title, body string, actions []Action) (string, error) {
	if timeout < -1 || timeout > math.MaxInt32 {
		return "", fmt.Errorf("notification timeout must be between -1 and %d milliseconds", math.MaxInt32)
	}
	lifetime, cancel := context.WithCancel(ctx)
	defer cancel()
	sendCtx, cancelSend := context.WithTimeout(lifetime, sendTimeout)
	defer cancelSend()

	conn, err := connect(sendCtx, lifetime)
	if err != nil {
		return "", fmt.Errorf("connect to notification bus: %w", callErr(sendCtx, err))
	}
	defer func() { _ = conn.Close() }()

	obj := conn.Object(service, objectPath)
	var capabilities []string
	if err := obj.CallWithContext(sendCtx, service+".GetCapabilities", 0).Store(&capabilities); err != nil {
		return "", fmt.Errorf("get notification capabilities: %w", callErr(sendCtx, err))
	}
	if slices.Contains(capabilities, "body-markup") {
		body = html.EscapeString(body)
	}
	if icon == "" {
		icon = desktopID
	} else if filepath.IsAbs(icon) {
		if _, err := os.Stat(icon); err != nil {
			icon = desktopID
		}
	}

	buttons := make([]string, 0, 2*len(actions))
	for _, action := range actions {
		buttons = append(buttons, action.ID, action.Label)
	}
	wait := len(actions) > 0 && slices.Contains(capabilities, "actions")
	var signals chan *dbus.Signal
	if wait {
		// Subscribe before Notify because an action can arrive before its method reply
		signals = make(chan *dbus.Signal, 16)
		conn.Signal(signals)
		if err := conn.AddMatchSignalContext(sendCtx, dbus.WithMatchSender(service), dbus.WithMatchObjectPath(objectPath), dbus.WithMatchInterface(service)); err != nil {
			return "", fmt.Errorf("subscribe to notification actions: %w", callErr(sendCtx, err))
		}
		if err := conn.AddMatchSignalContext(sendCtx, dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, service)); err != nil {
			return "", fmt.Errorf("watch notification service: %w", callErr(sendCtx, err))
		}
	}

	hints := map[string]dbus.Variant{
		"desktop-entry": dbus.MakeVariant(desktopID),
		"urgency":       dbus.MakeVariant(byte(1)),
	}
	if icon == desktopID {
		hints["image-data"], err = logoData()
		if err != nil {
			return "", err
		}
	}
	var id uint32
	if err := obj.CallWithContext(sendCtx, service+".Notify", 0, "Tequilashot", uint32(0), icon, title, body, buttons, hints, int32(timeout)).Store(&id); err != nil {
		return "", fmt.Errorf("send notification: %w", callErr(sendCtx, err))
	}
	cancelSend()
	if !wait {
		return "", nil
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(lifetime, sendTimeout)
		defer cancel()
		_ = obj.CallWithContext(closeCtx, service+".CloseNotification", dbus.FlagNoReplyExpected, id).Err
	}()

	waitTime := 30 * time.Second
	if timeout > 0 {
		waitTime = time.Duration(timeout) * time.Millisecond
	}
	timer := time.NewTimer(waitTime)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "", nil
		case signal, ok := <-signals:
			if !ok {
				return "", errors.New("notification bus disconnected")
			}
			if signal.Name == "org.freedesktop.DBus.NameOwnerChanged" {
				var name, oldOwner, newOwner string
				if dbus.Store(signal.Body, &name, &oldOwner, &newOwner) == nil && name == service && oldOwner != "" {
					return "", errors.New("notification service disconnected")
				}
				continue
			}
			var notificationID uint32
			switch signal.Name {
			case service + ".ActionInvoked":
				var actionID string
				if dbus.Store(signal.Body, &notificationID, &actionID) == nil && notificationID == id {
					for _, action := range actions {
						if action.ID == actionID {
							return actionID, nil
						}
					}
				}
			case service + ".NotificationClosed":
				var reason uint32
				if dbus.Store(signal.Body, &notificationID, &reason) == nil && notificationID == id {
					return "", nil
				}
			}
		}
	}
}

// callErr reports the context error when ctx has ended, since the connection
// is closed on cancellation and pending calls may fail with a socket error first
func callErr(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func connect(ctx, lifetime context.Context) (*dbus.Conn, error) {
	type result struct {
		conn *dbus.Conn
		err  error
	}
	ready := make(chan result)
	go func() {
		conn, err := dbus.ConnectSessionBus(dbus.WithContext(lifetime))
		select {
		case ready <- result{conn, err}:
		case <-ctx.Done():
			if conn != nil {
				_ = conn.Close()
			}
		}
	}()
	select {
	case result := <-ready:
		return result.conn, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// CaptureDelay announces countdowns longer than two seconds
func CaptureDelay(waitSeconds int, label, icon string) error {
	if waitSeconds > 2 {
		return Send((waitSeconds-1)*1000, icon, "Capture starting", fmt.Sprintf("Capturing %s in %d seconds", label, waitSeconds))
	}
	return nil
}
