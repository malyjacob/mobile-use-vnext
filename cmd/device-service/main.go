package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/android"
	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/engine"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/httpapi"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
	"github.com/malyjacob/mobile-use-vnext/internal/store"
)

func main() {
	os.Exit(run())
}

func run() int {
	configPath := flag.String("config", "/opt/mobile-use-vnext/config.json", "private service config")
	showVersion := flag.Bool("version", false, "print release version")
	flag.Parse()
	if *showVersion {
		fmt.Println(protocol.ReleaseVersion)
		return 0
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "device-service: configuration rejected: %v\n", err)
		return 2
	}

	// The fixed runtime-directory lock is acquired before the ledger, the App
	// handshake or any Android artifact is touched.
	lock, err := acquireProcessLock(cfg.StateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "device-service: process lock unavailable")
		return 3
	}
	defer lock.Release()

	instanceID := engine.NewUUID()
	logger := eventlog.New(os.Stderr, instanceID)
	ledger, err := store.New(cfg.StateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "device-service: ledger directory unavailable")
		return 4
	}
	deviceAdapter, err := android.New(android.Config{
		AdbPath:        cfg.ADB.Path,
		Serial:         cfg.ADB.Serial,
		U2Port:         cfg.ADB.U2Port,
		LocalJarPath:   cfg.U2JarPath,
		RemoteJarPath:  "/data/local/tmp/u2.jar",
		ControlPackage: cfg.ControlPackage,
		Timeout:        cfg.adbTimeout(),
	}, logger)
	if err != nil {
		logger.Event(eventlog.Error, "service.start_failed", eventlog.Str("code", "device_config"))
		return 5
	}

	engineConfig := engine.DefaultConfig(instanceID, cfg.AgentName)
	engineConfig.ControlPackage = cfg.ControlPackage
	eng := engine.New(engineConfig, logger,
		deviceAdapter, ledger,
		&observationProjector{builder: observation.NewBuilder()}, treeDiffer{})

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	// Read the durable ownership before serving anything; ordinary admission
	// stays closed until the device and paired App facts are valid.
	if err := eng.StartRecovery(ctx); err != nil {
		logger.Event(eventlog.Error, "recovery.failed", eventlog.Str("code", "recovery"))
	}
	eng.StartClock()
	eng.SetDeviceReady(false)

	server := httpapi.New(httpapi.Config{
		Host:              cfg.Server.Host,
		Port:              cfg.Server.Port,
		AgentToken:        cfg.Tokens.Agent,
		ControlToken:      cfg.Tokens.Control,
		AgentName:         cfg.AgentName,
		ServiceInstanceID: instanceID,
		ReleaseVersion:    cfg.ReleaseVersion,
	}, eng, logger)

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	logger.Event(eventlog.Info, "service.start", eventlog.Str("version", cfg.ReleaseVersion),
		eventlog.Bool("ready", false))

	go prepareDevice(ctx, cfg, deviceAdapter, eng, logger)

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil {
			logger.Event(eventlog.Error, "service.serve_failed", eventlog.Str("code", "listener"))
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	eng.Shutdown(shutdownCtx)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = deviceAdapter.StopU2(stopCtx)
	stopCancel()
	logger.Event(eventlog.Info, "service.stop")
	logger.Close()
	return 0
}

// prepareDevice starts the fixed u2 artifact only after the fixed JAR and ADB
// transport are verified. Failures keep ordinary admission closed.
func prepareDevice(ctx context.Context, cfg *Config, dev *android.Device,
	eng *engine.Engine, logger *eventlog.Logger) {
	for {
		if ctx.Err() != nil {
			return
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := dev.Prepare(attemptCtx)
		if err == nil {
			err = dev.EnsureU2(attemptCtx)
		}
		cancel()
		if err == nil {
			eng.SetDeviceReady(true)
			logger.Event(eventlog.Info, "device.ready")
			eng.RetryRecovery(context.Background())
			return
		}
		logger.Event(eventlog.Warn, "device.prepare_failed",
			eventlog.Str("code", android.CodeOf(err)))
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// observationProjector adapts the pure projection package to the engine's
// small consumer interface.
type observationProjector struct {
	builder *observation.Builder
}

func (p *observationProjector) Build(capture *device.Capture) (*engine.Projection, error) {
	snapshot, err := p.builder.Build(capture)
	if err != nil {
		return nil, err
	}
	return &engine.Projection{
		Tree: snapshot.Tree, Capture: snapshot.Capture, Source: snapshot.Source,
	}, nil
}

func (p *observationProjector) Reset() { p.builder.Reset() }

// treeDiffer adapts the pure delta algorithm to the engine interface.
type treeDiffer struct{}

func (treeDiffer) Diff(baseID string, base, current *protocol.Tree) (*protocol.Delta, error) {
	return observation.Diff(baseID, base, current)
}
