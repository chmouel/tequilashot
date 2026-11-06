package notify

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type notification struct {
	app, icon, title, body string
	replaces               uint32
	actions                []string
	hints                  map[string]dbus.Variant
	timeout                int32
}

type notificationService struct {
	conn         *dbus.Conn
	capabilities []string
	received     chan notification
	closed       chan uint32
	mu           sync.Mutex
	onNotify     func() *dbus.Error
}

func (s *notificationService) setNotify(fn func() *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onNotify = fn
}

func (s *notificationService) GetCapabilities() ([]string, *dbus.Error) {
	return s.capabilities, nil
}

func (s *notificationService) Notify(app string, replaces uint32, icon, title, body string, actions []string, hints map[string]dbus.Variant, timeout int32) (uint32, *dbus.Error) {
	s.received <- notification{app: app, replaces: replaces, icon: icon, title: title, body: body, actions: actions, hints: hints, timeout: timeout}
	s.mu.Lock()
	onNotify := s.onNotify
	s.mu.Unlock()
	if onNotify != nil {
		if err := onNotify(); err != nil {
			return 0, err
		}
	}
	return 42, nil
}

func (s *notificationService) CloseNotification(id uint32) *dbus.Error {
	s.closed <- id
	return nil
}

func (s *notificationService) emit(member string, body ...any) *dbus.Error {
	if err := s.conn.Emit(objectPath, service+"."+member, body...); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

func privateBus(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("notification integration tests require dbus-daemon")
	}
	config := filepath.Join(t.TempDir(), "bus.conf")
	// Exclude desktop service directories so tests cannot activate the real notification daemon
	if err := os.WriteFile(config, []byte(`<busconfig>
  <type>session</type>
  <listen>unix:tmpdir=/tmp</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*"/>
    <allow receive_sender="*"/>
    <allow own="*"/>
  </policy>
</busconfig>`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("dbus-daemon", "--config-file="+config, "--nofork", "--print-address=1") //nolint:gosec
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("read private bus address: %v", scanner.Err())
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", scanner.Text())
}

func newNotificationService(t *testing.T, capabilities ...string) *notificationService {
	t.Helper()
	privateBus(t)
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	s := &notificationService{conn: conn, capabilities: capabilities, received: make(chan notification, 4), closed: make(chan uint32, 4)}
	if err := conn.Export(s, objectPath, service); err != nil {
		t.Fatal(err)
	}
	if reply, err := conn.RequestName(service, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own notification service: %v, %v", reply, err)
	}
	return s
}

func TestNotificationPresentation(t *testing.T) {
	for _, markup := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "markup"}[markup], func(t *testing.T) {
			capabilities := []string{"body"}
			if markup {
				capabilities = append(capabilities, "body-markup")
			}
			s := newNotificationService(t, capabilities...)
			icon := filepath.Join(t.TempDir(), "capture.svg")
			if err := os.WriteFile(icon, []byte("<svg/>"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, requestedIcon := range []string{icon, "", filepath.Join(t.TempDir(), "missing.svg")} {
				if err := Send(3000, requestedIcon, "Screenshot saved", "capture <one> & two.png"); err != nil {
					t.Fatal(err)
				}
				n := <-s.received
				wantIcon := desktopID
				if requestedIcon == icon {
					wantIcon = icon
				}
				wantBody := "capture <one> & two.png"
				if markup {
					wantBody = "capture &lt;one&gt; &amp; two.png"
				}
				if n.app != "Tequilashot" || n.replaces != 0 || n.icon != wantIcon || n.title != "Screenshot saved" || n.body != wantBody || n.timeout != 3000 || len(n.actions) != 0 {
					t.Fatalf("unexpected notification: %+v", n)
				}
				if n.hints["desktop-entry"].Value() != desktopID || n.hints["urgency"].Value() != byte(1) {
					t.Fatalf("unexpected hints: %v", n.hints)
				}
				logo, hasLogo := n.hints["image-data"]
				if hasLogo != (wantIcon == desktopID) {
					t.Fatalf("embedded logo present = %v for icon %q", hasLogo, requestedIcon)
				}
				if hasLogo {
					var pixels notificationImage
					if err := dbus.Store([]any{logo.Value()}, &pixels); err != nil {
						t.Fatal(err)
					}
					if pixels.Width != 128 || pixels.Height != 128 || pixels.Rowstride != 512 || !pixels.HasAlpha || pixels.BitsPerSample != 8 || pixels.Channels != 4 || len(pixels.Data) != 128*128*4 {
						t.Fatalf("invalid logo data: signature=%s size=%dx%d stride=%d alpha=%v bits=%d channels=%d bytes=%d", logo.Signature(), pixels.Width, pixels.Height, pixels.Rowstride, pixels.HasAlpha, pixels.BitsPerSample, pixels.Channels, len(pixels.Data))
					}
					var ivory, amber bool
					for i := 0; i < len(pixels.Data); i += 4 {
						switch [4]byte(pixels.Data[i : i+4]) {
						case [4]byte{0xff, 0xf3, 0xd6, 0xff}:
							ivory = true
						case [4]byte{0xff, 0xb3, 0x2c, 0xff}:
							amber = true
						}
					}
					if !ivory || !amber {
						t.Fatal("notification logo is missing its ivory or amber foreground")
					}
				}
			}
		})
	}
}

func TestNotificationActions(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		closed     bool
	}{
		{name: "immediate action", want: "copy"},
		{name: "dismissal", closed: true},
		{name: "local expiry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newNotificationService(t, "actions")
			s.setNotify(func() *dbus.Error {
				for _, signal := range []struct {
					member string
					body   []any
				}{
					{"ActionInvoked", []any{uint32(99), "copy"}},
					{"NotificationClosed", []any{uint32(99), uint32(2)}},
					{"ActionInvoked", []any{uint32(42), "unknown"}},
					{"ActionInvoked", []any{"malformed"}},
				} {
					if err := s.emit(signal.member, signal.body...); err != nil {
						return err
					}
				}
				if tc.want != "" {
					return s.emit("ActionInvoked", uint32(42), tc.want)
				}
				if tc.closed {
					return s.emit("NotificationClosed", uint32(42), uint32(2))
				}
				return nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			action, err := send(ctx, 100, "", "Screenshot saved", "shot.png", []Action{{ID: "copy", Label: "Copy image"}, {ID: "edit", Label: "Edit"}})
			if err != nil || action != tc.want {
				t.Fatalf("action = %q, error = %v", action, err)
			}
			if n := <-s.received; !reflect.DeepEqual(n.actions, []string{"copy", "Copy image", "edit", "Edit"}) {
				t.Fatalf("unexpected action order: %v", n.actions)
			}
			select {
			case id := <-s.closed:
				if id != 42 {
					t.Fatalf("closed notification %d", id)
				}
			case <-ctx.Done():
				t.Fatal("notification was not closed")
			}
		})
	}
}

func TestNotificationWithoutActionSupport(t *testing.T) {
	s := newNotificationService(t, "body")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	action, err := send(ctx, 30000, "", "Recording saved", "clip.mp4", []Action{{ID: "show", Label: "Show"}})
	if err != nil || action != "" {
		t.Fatalf("action = %q, error = %v", action, err)
	}
	if n := <-s.received; n.title != "Recording saved" {
		t.Fatalf("notification was not delivered: %+v", n)
	}
}

func TestNotificationFailures(t *testing.T) {
	t.Run("unavailable service", func(t *testing.T) {
		privateBus(t)
		if err := Send(3000, "", "Screenshot saved", "shot.png"); err == nil {
			t.Fatal("missing notification service reported success")
		}
	})
	t.Run("rejected notification", func(t *testing.T) {
		s := newNotificationService(t)
		s.setNotify(func() *dbus.Error { return dbus.MakeFailedError(errors.New("refused")) })
		if err := Send(3000, "", "Screenshot saved", "shot.png"); err == nil || !strings.Contains(err.Error(), "refused") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("stalled notification", func(t *testing.T) {
		s := newNotificationService(t)
		unblock := make(chan struct{})
		defer close(unblock)
		s.setNotify(func() *dbus.Error { <-unblock; return nil })
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if _, err := send(ctx, 3000, "", "Screenshot saved", "shot.png", nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("service disappears during action wait", func(t *testing.T) {
		s := newNotificationService(t, "actions")
		s.setNotify(func() *dbus.Error {
			_, err := s.conn.ReleaseName(service)
			if err != nil {
				return dbus.MakeFailedError(err)
			}
			return nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := send(ctx, 30000, "", "Recording saved", "clip.mp4", []Action{{ID: "show", Label: "Show"}})
		if err == nil || !strings.Contains(err.Error(), "notification service disconnected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("stalled bus handshake", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bus")
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = listener.Close() }()
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+path)
		accepted := make(chan net.Conn, 1)
		go func() {
			conn, err := listener.Accept()
			if err == nil {
				accepted <- conn
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if _, err := send(ctx, 3000, "", "Screenshot saved", "shot.png", nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unexpected error: %v", err)
		}
		select {
		case conn := <-accepted:
			_ = conn.Close()
		case <-time.After(time.Second):
			t.Fatal("bus connection was not attempted")
		}
	})
}

func TestCaptureDelay(t *testing.T) {
	s := newNotificationService(t, "body")
	if err := CaptureDelay(2, "screenshot", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.received:
		t.Fatal("short countdown sent a notification")
	default:
	}
	if err := CaptureDelay(5, "screenshot", ""); err != nil {
		t.Fatal(err)
	}
	n := <-s.received
	if n.title != "Capture starting" || n.body != "Capturing screenshot in 5 seconds" || n.timeout != 4000 {
		t.Fatalf("unexpected countdown: %+v", n)
	}
}
