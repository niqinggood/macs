package app

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	channelcore "mac/internal/channels"
	"mac/internal/eventbus"
)

type gatewayStartRollbackChannel struct {
	starts atomic.Int32
	stops  atomic.Int32
}

func (c *gatewayStartRollbackChannel) Name() string                     { return "rollback-test" }
func (c *gatewayStartRollbackChannel) Type() string                     { return "test" }
func (c *gatewayStartRollbackChannel) Start() error                     { c.starts.Add(1); return nil }
func (c *gatewayStartRollbackChannel) Stop() error                      { c.stops.Add(1); return nil }
func (c *gatewayStartRollbackChannel) SendMessage(string, string) error { return nil }
func (c *gatewayStartRollbackChannel) SetGateway(*GatewayService)       {}
func (c *gatewayStartRollbackChannel) SetRuntime(channelcore.Runtime)   {}

func reserveGatewayStartTestAddress(t *testing.T) (net.Listener, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	return listener, listener.Addr().String()
}

func availableGatewayStartTestAddress(t *testing.T) string {
	t.Helper()
	listener, address := reserveGatewayStartTestAddress(t)
	if err := listener.Close(); err != nil {
		t.Fatalf("release address %s: %v", address, err)
	}
	return address
}

func newGatewayStartRollbackScenario(
	t *testing.T,
	mainAddress, agentOSAddress string,
) (*GatewayService, *gatewayStartRollbackChannel) {
	t.Helper()
	logger := log.New(io.Discard, "", 0)
	config := &Config{
		Workspace: t.TempDir(),
		Headless:  true,
		Gateway: GatewayConfig{
			Enabled:           true,
			ListenAddr:        mainAddress,
			AgentOSListenAddr: agentOSAddress,
			AgentOSMode:       "gateway",
		},
	}
	gateway := &GatewayService{
		config:   config,
		logger:   logger,
		project:  NewProjectContext(config.Workspace),
		tasks:    make(map[string]*GatewayTask),
		eventBus: eventbus.New(logger),
	}
	gateway.initSSE()
	gateway.channelManager = NewChannelManager(gateway)
	channel := &gatewayStartRollbackChannel{}
	if err := gateway.channelManager.RegisterChannel(channel); err != nil {
		t.Fatalf("register rollback channel: %v", err)
	}
	return gateway, channel
}

func assertGatewayFailedStartRolledBack(
	t *testing.T,
	gateway *GatewayService,
	channel *gatewayStartRollbackChannel,
	expectedChannelStarts int32,
) {
	t.Helper()
	if got := channel.starts.Load(); got != expectedChannelStarts {
		t.Fatalf("channel starts = %d, want %d", got, expectedChannelStarts)
	}
	if got := channel.stops.Load(); got != expectedChannelStarts {
		t.Fatalf("channel stops = %d, want %d", got, expectedChannelStarts)
	}
	if gateway.httpServer != nil || gateway.agentOSHTTPServer != nil {
		t.Fatalf("server state leaked: main=%p agentos=%p", gateway.httpServer, gateway.agentOSHTTPServer)
	}
	gateway.backgroundMu.Lock()
	backgroundCancel := gateway.backgroundCancel
	gateway.backgroundMu.Unlock()
	if backgroundCancel != nil {
		t.Fatal("background lifecycle remained active after failed Start")
	}
	queue := gateway.ensureAgentOSQueue()
	queue.mu.Lock()
	started, queueContext, queueCancel := queue.started, queue.ctx, queue.cancel
	queue.mu.Unlock()
	if started || queueContext != nil || queueCancel != nil {
		t.Fatalf("queue lifecycle leaked: started=%v ctx=%v cancel=%v", started, queueContext != nil, queueCancel != nil)
	}
	if queue.accepting.Load() || queue.draining.Load() {
		t.Fatalf("queue admission state leaked: accepting=%v draining=%v", queue.accepting.Load(), queue.draining.Load())
	}
}

func assertGatewayStartTestAddressReusable(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		listener, err := net.Listen("tcp", address)
		if err == nil {
			_ = listener.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("address %s was not released: %v", address, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForGatewayStartTestListener(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Gateway did not listen on %s: %v", address, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGatewayStartRollsBackWhenDedicatedAgentOSBindFails(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("OPENKITTY_FIXED_PORT", "true")
	t.Setenv("OPENKITTY_DISABLE_HTTPS", "true")
	t.Setenv(envDisableChannels, "false")

	occupiedAgentOS, agentOSAddress := reserveGatewayStartTestAddress(t)
	defer occupiedAgentOS.Close()
	mainAddress := availableGatewayStartTestAddress(t)
	gateway, channel := newGatewayStartRollbackScenario(t, mainAddress, agentOSAddress)

	if err := gateway.Start(); err == nil {
		t.Fatal("Start succeeded with occupied AgentOS address")
	}

	assertGatewayFailedStartRolledBack(t, gateway, channel, 0)
	assertGatewayStartTestAddressReusable(t, mainAddress)
}

func TestGatewayStartRollsBackDedicatedServerWhenMainBindFails(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("OPENKITTY_FIXED_PORT", "true")
	t.Setenv("OPENKITTY_DISABLE_HTTPS", "true")
	t.Setenv(envDisableChannels, "false")

	occupiedMain, mainAddress := reserveGatewayStartTestAddress(t)
	defer occupiedMain.Close()
	agentOSAddress := availableGatewayStartTestAddress(t)
	gateway, channel := newGatewayStartRollbackScenario(t, mainAddress, agentOSAddress)

	err := gateway.Start()
	if err == nil || err == http.ErrServerClosed {
		t.Fatalf("Start error = %v, want bind failure", err)
	}

	assertGatewayFailedStartRolledBack(t, gateway, channel, 0)
	assertGatewayStartTestAddressReusable(t, agentOSAddress)

	// A failed Start is retryable: after the original conflict is released,
	// the queue can be started and stopped cleanly again.
	if err := occupiedMain.Close(); err != nil {
		t.Fatalf("release main address: %v", err)
	}
	queue := gateway.ensureAgentOSQueue()
	queue.Start()
	if !queue.accepting.Load() {
		t.Fatal("queue did not become accepting after failed-start rollback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	queue.Stop(ctx)
}

func TestGatewayStartNormalStopDoesNotRunFailedStartRollbackTwice(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("OPENKITTY_FIXED_PORT", "true")
	t.Setenv("OPENKITTY_DISABLE_HTTPS", "true")
	t.Setenv(envDisableChannels, "false")

	mainAddress := availableGatewayStartTestAddress(t)
	gateway, channel := newGatewayStartRollbackScenario(t, mainAddress, "")
	startResult := make(chan error, 1)
	go func() {
		startResult <- gateway.Start()
	}()
	waitForGatewayStartTestListener(t, mainAddress)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := gateway.Stop(ctx); err != nil {
		cancel()
		t.Fatalf("normal Stop: %v", err)
	}
	cancel()
	select {
	case err := <-startResult:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Start returned %v after normal Stop", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after normal Stop")
	}
	if got := channel.starts.Load(); got != 1 {
		t.Fatalf("channel starts = %d, want 1", got)
	}
	if got := channel.stops.Load(); got != 1 {
		t.Fatalf("channel stops = %d, failed-start rollback ran after normal Stop", got)
	}
	gateway.backgroundMu.Lock()
	backgroundCancel := gateway.backgroundCancel
	gateway.backgroundMu.Unlock()
	if backgroundCancel != nil {
		t.Fatal("normal Stop left background lifecycle active")
	}
	assertGatewayStartTestAddressReusable(t, mainAddress)
}
