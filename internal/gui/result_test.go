//go:build !nogui

package gui

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type showItemsRequest struct {
	uris      []string
	startupID string
}

type fileManagerServiceForTest struct {
	received chan showItemsRequest
	unblock  <-chan struct{}
}

func (s *fileManagerServiceForTest) ShowItems(uris []string, startupID string) *dbus.Error {
	s.received <- showItemsRequest{uris: uris, startupID: startupID}
	if s.unblock != nil {
		<-s.unblock
	}
	return nil
}

func privateSessionBus(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("D-Bus integration tests require dbus-daemon")
	}
	config := filepath.Join(t.TempDir(), "bus.conf")
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

func newFileManagerService(t *testing.T, unblock <-chan struct{}) *fileManagerServiceForTest {
	t.Helper()
	privateSessionBus(t)
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	service := &fileManagerServiceForTest{received: make(chan showItemsRequest, 1), unblock: unblock}
	if err := conn.Export(service, fileManagerObjectPath, fileManagerService); err != nil {
		t.Fatal(err)
	}
	if reply, err := conn.RequestName(fileManagerService, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own file manager service: %v, %v", reply, err)
	}
	return service
}

func TestShowItems(t *testing.T) {
	service := newFileManagerService(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := showItems(ctx, "file:///tmp/recording%20one.mp4"); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-service.received:
		if len(request.uris) != 1 || request.uris[0] != "file:///tmp/recording%20one.mp4" || request.startupID != "" {
			t.Fatalf("unexpected ShowItems request: %+v", request)
		}
	case <-ctx.Done():
		t.Fatal("ShowItems was not received")
	}
}

func TestShowItemsHonoursContext(t *testing.T) {
	unblock := make(chan struct{})
	defer close(unblock)
	service := newFileManagerService(t, unblock)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := showItems(ctx, "file:///tmp/recording.mp4")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("showItems() error = %v; want context deadline", err)
	}
	select {
	case <-service.received:
	case <-time.After(time.Second):
		t.Fatal("ShowItems was not received")
	}
}
