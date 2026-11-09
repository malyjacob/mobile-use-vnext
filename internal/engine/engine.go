// Package engine is the single business authority for sessions, observations,
// operations, the ordinary device work position, STOP and cleanup. It holds no
// device I/O implementation: the small interfaces below are supplied by cmd.
package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Session and observation states.
const (
	SessionStarting = "starting"
	SessionActive   = "active"
	SessionEnded    = "ended"

	ObservationReady   = "ready"
	ObservationClaimed = "claimed"
	ObservationRetired = "retired"
	ObservationExpired = "expired"

	OperationRunning  = "running"
	OperationFinished = "finished"
)

// Availability states.
const (
	AvailabilityReady      = "ready"
	AvailabilityRecovering = "recovering"
	AvailabilityBlocked    = "blocked"
)

// Work kinds.
const (
	WorkStart   = "start"
	WorkObserve = "observe"
	WorkAct     = "act"
	WorkApps    = "apps"
	WorkOpenApp = "open_app"
	WorkShell   = "shell"
)

// Outcome values.
const (
	OutcomeExecuted    = "executed"
	OutcomeNotExecuted = "not_executed"
	OutcomeUncertain   = "uncertain"
)

// Config is the fixed internal policy for the first writable loop. These are
// service policy, not per-call options.
type Config struct {
	ServiceInstanceID   string
	AgentName           string
	SessionTTL          time.Duration // 120s sliding activity
	SessionHardLimit    time.Duration // 1h from start
	ObservationLifetime time.Duration // 60s input lifetime
	StartTimeout        time.Duration // 20s create phase
	WorkTimeout         time.Duration // 20s per capture/action phase
	CaptureMaxAge       time.Duration // 15s publish age
	AppFreshness        time.Duration // 6s control heartbeat freshness
	CleanupTimeout      time.Duration // 12s physical cleanup wait
	LedgerTimeout       time.Duration // 3s durable write wait
	MaxFullTreeBytes    int           // 128 KiB canonical full tree
	MaxObservations     int           // bounded retained observations
	MaxOperations       int           // bounded P records
	MaxAcceptanceAge    time.Duration // 5min P acceptance window
	ControlPackage      string        // paired control App package
	MaxAppsBytes        int           // 32 KiB apps result budget
	AppListTTL          time.Duration // 60s trusted app-list cache
	ShellTimeout        time.Duration // 30s managed shell execution budget
	ShellConfirmTimeout time.Duration // 5s termination/confirmation budget
	ShellStartTimeout   time.Duration // 3s managed start evidence budget
	ShellOutputBytes    int           // 64 KiB retained output prefix

	// Image policy. The image is additional material of one acquisition, so
	// these are fixed service budgets, never per-call options.
	ImageMaxLongSide int           // longest output edge in pixels (never upscaled)
	ImageQuality     int           // JPEG quality for the single output encode
	MaxImageBytes    int           // encoded image byte budget
	ImageMaxSpan     time.Duration // allowed image-read start to tree-read start span

	// Settle policy. A post observation waits for a converged tree before it
	// is published; the per-phase values are service policy, never per-call
	// options.
	Settle SettlePolicies
}

// DefaultConfig returns the first-version policy values.
func DefaultConfig(instanceID, agentName string) Config {
	return Config{
		ServiceInstanceID:   instanceID,
		AgentName:           agentName,
		SessionTTL:          120 * time.Second,
		SessionHardLimit:    time.Hour,
		ObservationLifetime: 60 * time.Second,
		StartTimeout:        20 * time.Second,
		WorkTimeout:         20 * time.Second,
		CaptureMaxAge:       15 * time.Second,
		AppFreshness:        6 * time.Second,
		CleanupTimeout:      12 * time.Second,
		LedgerTimeout:       3 * time.Second,
		MaxFullTreeBytes:    128 * 1024,
		MaxObservations:     32,
		MaxOperations:       256,
		MaxAcceptanceAge:    5 * time.Minute,
		ControlPackage:      "com.mobileuse.vnext",
		MaxAppsBytes:        32 * 1024,
		AppListTTL:          60 * time.Second,
		ShellTimeout:        30 * time.Second,
		ShellConfirmTimeout: 5 * time.Second,
		ShellStartTimeout:   3 * time.Second,
		ShellOutputBytes:    65536,
		ImageMaxLongSide:    1200,
		ImageQuality:        80,
		MaxImageBytes:       512 * 1024,
		ImageMaxSpan:        3 * time.Second,
		Settle:              DefaultSettlePolicies(),
	}
}

// Device is the Android adapter capability consumed by the engine. The
// concrete implementation lives in internal/android.
type Device interface {
	Prepare(ctx context.Context) error
	EnsureU2(ctx context.Context) error
	StopU2(ctx context.Context) error
	LogicalSize(ctx context.Context) (width, height, density int, err error)
	ProbeDisplay(ctx context.Context, displayID int) (device.DisplayState, error)
	Capture(ctx context.Context, display device.Display) (device.Capture, error)
	// CaptureImage reads one raw source image bound to the current target
	// display surface. The engine decides whether to attach, discard or report
	// it; the adapter never encodes or publishes it.
	CaptureImage(ctx context.Context, sfid uint64) (device.ImageSource, error)
	Foreground(ctx context.Context, displayID int) (device.Foreground, error)
	TaskList(ctx context.Context) ([]device.Task, error)
	Tap(ctx context.Context, displayID, x, y int) error
	LongPress(ctx context.Context, displayID, x, y int) error
	Swipe(ctx context.Context, displayID, x1, y1, x2, y2 int) error
	Key(ctx context.Context, displayID int, key string) error
	GetTextOnDisplay(ctx context.Context, target device.TextTarget) (device.TextResult, error)
	SetTextOnDisplay(ctx context.Context, target device.TextTarget, text string) (device.TextResult, error)
	StartDisplayHelper(ctx context.Context, commandID, appInstanceID string) error
	AppList(ctx context.Context) ([]device.AppEntry, error)
	ResolveLauncher(ctx context.Context, pkg string) (string, bool, error)
	LaunchApp(ctx context.Context, displayID int, component string) (device.LaunchResult, error)
	FocusTask(ctx context.Context, taskID int) error
	RemoveTask(ctx context.Context, taskID int) error
	RecentTaskIDs(ctx context.Context) (map[int]bool, bool)
	TasksOnDisplay(ctx context.Context, displayID int) ([]device.Task, bool)
	// Managed shell runner. The concrete adapter writes the exact command
	// bytes, starts the detached runner and reports bounded evidence; the
	// engine owns admission, ownership persistence and the outcome mapping.
	PrepareManagedShell(ctx context.Context, runID string, command []byte) error
	StartManagedShell(ctx context.Context, runID string, root bool, deadline time.Duration) (device.ShellStart, error)
	InspectManagedShell(ctx context.Context, runID string) (device.ShellSnapshot, error)
	ReadManagedShellOutput(ctx context.Context, runID string) ([]byte, error)
	TerminateManagedShell(ctx context.Context, runID string, root bool, reason string) (device.ShellTermination, error)
	RemoveManagedShell(ctx context.Context, runID string, root bool) error
	RootAvailable(ctx context.Context) (bool, error)
}

// Store is the recovery ledger capability.
type Store interface {
	Load() (*Ledger, error)
	Save(ctx context.Context, record *Ledger) (int64, error)
}

// Projection is one public tree plus its immutable source capture mapping.
type Projection struct {
	Tree    *protocol.Tree
	Capture *device.Capture
	Source  map[string]int
}

// Projector builds the public tree and keeps conservative ref continuity.
type Projector interface {
	Build(capture *device.Capture) (*Projection, error)
	// Preview projects a candidate frame without advancing ref continuity, so
	// a settle sample never shifts the refs of already published observations.
	Preview(capture *device.Capture) (*Projection, error)
	Reset()
}

// Differ computes and applies the fixed-baseline delta.
type Differ interface {
	Diff(baseID string, base, current *protocol.Tree) (*protocol.Delta, error)
}

// ImageEncoder turns one source image into the single bounded output image.
// It is a pure algorithm: it performs no device I/O and publishes nothing. The
// returned metadata carries the actual output size, MIME type and source read
// start; the engine alone decides whether the image belongs to an observation.
type ImageEncoder interface {
	Encode(source device.ImageSource, display device.Display, maxLongSide, quality, maxBytes int) (*protocol.ImageMeta, []byte, error)
}

// Option customizes engine capabilities that are not part of every test.
type Option func(*Engine)

// WithImageEncoder supplies the pure image encoder used by observe/act when
// the caller asked for an image.
func WithImageEncoder(enc ImageEncoder) Option {
	return func(e *Engine) { e.imaging = enc }
}

// Engine is the single authority. All exported methods are safe for concurrent
// use; device I/O always happens outside the state lock.
type Engine struct {
	cfg       Config
	log       *eventlog.Logger
	dev       Device
	store     Store
	projector Projector
	differ    Differ
	imaging   ImageEncoder

	mu               sync.Mutex
	persistMu        sync.Mutex // serializes snapshot + Save, never held with state I/O
	messagePersistMu sync.Mutex // serializes message-box snapshot + Save
	messageStore     MessageStore

	startedAt       time.Time
	blockedReason   string // permanent blocker (corrupt ledger)
	recovering      bool
	recovery        *Ledger // original ledger evidence being resolved
	recoveryReason  string
	recoveryLoading bool
	recoveryRunning bool
	deviceReady     bool

	app       appState
	appNotify chan struct{}

	commands       []*appCommand
	closeAttempted map[displayOwner]bool
	stopBoundary   *stopBoundaryState
	stopSeqByApp   map[string]int64
	closed         map[string]time.Time
	closedOrder    []string

	session      *sessionState
	lastSession  *sessionState
	baseline     *baselineState
	current      *observationState
	observations map[string]*observationState
	operations   map[string]*operationState
	work         *workState
	cleanup      *cleanupState
	// shell is the current managed runner ownership. It outlives the ordinary
	// work slot when the chain is unresolved so admission stays closed.
	shell        *shellState
	shellProbing bool

	nextObservation int64
	nextOperation   int64
	nextCommand     int64
	nextSession     int64
	nextMessage     int64

	// messages is the bounded message queue behind notify. It never reaches the
	// ledger and holds no execution authority.
	messages           []messageState
	messageBytes       int
	messageSaveFailing bool
	appsCache          *appListCache

	lastControl     *protocol.ControlView
	lastFingerprint *controlComparable
	controlRev      int64
	ledgerUnsafe    bool

	shuttingDown bool
	shutdownOnce sync.Once
	tickerDone   chan struct{}
	wg           sync.WaitGroup
}

type appState struct {
	InstanceID      string
	Version         string
	Mode            string
	ModeGeneration  string
	ModeReady       bool
	StopReady       bool
	Notifications   bool
	Background      bool
	Power           *bool
	Keyguard        *bool
	Primary         *device.Display
	Display         *device.Display
	DisplayRaw      *protocol.AppDisplay
	LastSync        time.Time
	StopPending     *protocol.AppStopPending
	ContractMatched bool
}

type sessionState struct {
	Origin           string
	Agent            string
	Mode             string
	State            string
	StartedAt        time.Time
	ExpiresAt        time.Time
	HardExpiresAt    time.Time
	EndedAt          time.Time
	Reason           string
	Display          device.Display
	AppInstanceID    string
	ModeGeneration   string
	Epoch            int64
	Revision         int
	CreateCommand    string
	CreateResult     *bool
	CreateError      string
	ExpireTimer      *time.Timer
	CleanupStarted   bool
	ExecutionUnknown bool // survives release of an uncertain ordinary work slot
	// Tasks is the virtual-session window registry. It is a cache rebuilt from
	// live task reads, keyed by task id; primary sessions never populate it.
	Tasks []taskEntry
}

type baselineState struct {
	ObservationID string
	Origin        string
	Revision      int
	CapturedAt    time.Time
	Tree          *protocol.Tree
}

type observationState struct {
	ID                string
	Origin            string
	Revision          int
	Agent             string
	SessionEpoch      int64
	DisplayID         int
	DisplayGeneration string
	CapturedAt        time.Time
	ActionableUntil   time.Time
	Screen            device.Display
	Foreground        device.Foreground
	Projection        *Projection
	Status            string
	ClaimedBy         string
	RetiredAt         time.Time
	Full              bool
	Error             *protocol.Error
}

type operationState struct {
	ID            string
	Agent         string
	Kind          string
	State         string
	Phase         string
	Outcome       string
	Error         *protocol.Error
	SessionOrigin string
	CreatedAt     time.Time
	FinishedAt    time.Time
	// Reference is the kind of reference the request already used ("O"/"P").
	// It is a planning fact only: it never grants authority.
	Reference string
	// Claimed records the one-way ready->claimed transition of this operation.
	// It outlives the work position so status(P) can replay the same plan.
	Claimed bool
	// InputState is the explicit observation usability a path knows but cannot
	// derive from code and claim alone (start's first observation). It is
	// consumed once, when the operation finishes.
	InputState string
	// Recovery is the projection returned with this operation, kept so a
	// duplicate P or status(P) reports exactly the same next step.
	Recovery *protocol.Recovery
}

type workState struct {
	OperationID  string
	Agent        string
	Kind         string
	Phase        string
	SessionEpoch int64
	Start        time.Time
	Revoked      bool
	Ctx          context.Context
	Cancel       context.CancelFunc
	ClaimedO     string
	// DispatchUnknown records input that may have reached Android without a
	// confirmed completion. Session cleanup stays unconfirmed while set.
	DispatchUnknown bool
}

type displayOwner struct {
	appInstance string
	displayID   int
	generation  string
}

type appCommand struct {
	Command     protocol.AppCommand
	RequestedAt time.Time
	DeliveredAt time.Time
	Result      *bool
	ErrorCode   string
	Kind        string
	AppInstance string
	// Ready is false between the durable intent and the successful root helper
	// bootstrap; the command is never delivered before it is true.
	Ready bool
}

type stopBoundaryState struct {
	AppInstanceID string
	Seq           int64
	AcceptedAt    time.Time
	State         string // revoked / recorded
}

type cleanupState struct {
	State     string
	Execution string
	Tasks     string
	Recents   string
	Display   string
	Reason    string
	// ExecReason records where the execution uncertainty came from:
	// ui_input (sticky, as before) or shell_chain (release-of-barrier
	// evidence can later confirm it). It is not a public field.
	ExecReason string
	StartedAt  time.Time
	Error      *protocol.Error
	done       chan struct{}
}

// New constructs an engine with the real monotonic clock.
func New(cfg Config, log *eventlog.Logger, dev Device, st Store, projector Projector, differ Differ, opts ...Option) *Engine {
	e := &Engine{
		cfg:            cfg,
		log:            log,
		dev:            dev,
		store:          st,
		projector:      projector,
		differ:         differ,
		startedAt:      time.Now(),
		appNotify:      make(chan struct{}),
		closeAttempted: map[displayOwner]bool{},
		stopSeqByApp:   map[string]int64{},
		closed:         map[string]time.Time{},
		observations:   map[string]*observationState{},
		operations:     map[string]*operationState{},
		tickerDone:     make(chan struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
	return e
}

// InstanceID returns this service instance identity.
func (e *Engine) InstanceID() string { return e.cfg.ServiceInstanceID }

// Availability returns the current control availability projection.
func (e *Engine) Availability() (string, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	state, reason := e.availabilityLocked(time.Now())
	return state, reason
}

func (e *Engine) availabilityLocked(now time.Time) (string, string) {
	if e.blockedReason != "" {
		return AvailabilityBlocked, e.blockedReason
	}
	if e.recovering || e.recovery != nil {
		if e.recoveryReason != "" {
			return AvailabilityRecovering, e.recoveryReason
		}
		return AvailabilityRecovering, "recovery_pending"
	}
	if e.cleanup != nil {
		switch e.cleanup.State {
		case protocol.CleanupPending:
			// Closure commands must still reach the paired App; ordinary start
			// remains blocked by canStart's session/cleanup checks.
			return AvailabilityRecovering, "cleanup_pending"
		case protocol.CleanupUnconfirmed:
			return AvailabilityBlocked, protocol.CodeCleanupUnconfirmed
		}
	}
	// An unresolved managed shell chain keeps the barrier closed: no new
	// ordinary work may be admitted until the exact runner is confirmed gone.
	if e.shell != nil && !e.shell.settled() {
		if e.shell.State == ShellTerminating {
			return AvailabilityRecovering, "shell_chain_unconfirmed"
		}
		return AvailabilityRecovering, "shell_pending"
	}
	if e.ledgerUnsafe {
		return AvailabilityBlocked, "ledger_unavailable"
	}
	if !e.deviceReady {
		return AvailabilityRecovering, protocol.CodeDeviceUnavailable
	}
	// The control foundation also requires the paired App's local stop/mode
	// facts. This is not device idleness: an active session can coexist.
	if e.app.LastSync.IsZero() {
		return AvailabilityRecovering, "control_sync_pending"
	}
	if !e.app.Background || !e.app.Notifications || !e.app.StopReady {
		return AvailabilityBlocked, protocol.CodeStopUnavailable
	}
	if !e.app.ModeReady {
		return AvailabilityBlocked, protocol.CodeModeNotSelected
	}
	if e.app.Power != nil && !*e.app.Power {
		return AvailabilityBlocked, protocol.CodeDeviceNotAwake
	}
	if e.app.Keyguard != nil && *e.app.Keyguard {
		return AvailabilityBlocked, protocol.CodeDeviceLocked
	}
	return AvailabilityReady, ""
}

// SetDeviceReady records the result of ADB/u2 preparation. A missing device
// keeps ordinary admission closed while control sync and status stay available.
func (e *Engine) SetDeviceReady(ready bool) {
	e.mu.Lock()
	e.deviceReady = ready
	e.broadcastAppLocked()
	e.mu.Unlock()
}

func (e *Engine) appFreshLocked(now time.Time) bool {
	return !e.app.LastSync.IsZero() && now.Sub(e.app.LastSync) <= e.cfg.AppFreshness
}

func (e *Engine) appReadyLocked(now time.Time) (bool, string) {
	if !e.appFreshLocked(now) {
		return false, protocol.CodeControlUnavailable
	}
	if !e.app.Background || !e.app.Notifications || !e.app.StopReady {
		return false, protocol.CodeStopUnavailable
	}
	if !e.app.ModeReady {
		return false, protocol.CodeModeNotSelected
	}
	if e.app.Power != nil && !*e.app.Power {
		return false, protocol.CodeDeviceNotAwake
	}
	if e.app.Keyguard != nil && *e.app.Keyguard {
		return false, protocol.CodeDeviceLocked
	}
	switch e.app.Mode {
	case "virtual":
		return true, ""
	case "primary":
		if e.app.Primary == nil || e.app.Primary.ID != 0 {
			return false, protocol.CodeDisplayUnavailable
		}
		if e.app.ModeGeneration == "" || e.app.Primary.Generation != e.app.ModeGeneration {
			return false, protocol.CodeModeNotSelected
		}
		return true, ""
	default:
		return false, protocol.CodeModeNotSelected
	}
}

// canStartLocked is the full start admission gate including the work position.
func (e *Engine) canStartLocked(now time.Time) (bool, string) {
	if e.work != nil {
		return false, protocol.CodeDeviceBusy
	}
	return e.canStartStateLocked(now)
}

// canStartStateLocked checks everything except the caller's own work position.
// Start holds the position while creating the session, so its second admission
// check must use this variant.
func (e *Engine) canStartStateLocked(now time.Time) (bool, string) {
	state, reason := e.availabilityLocked(now)
	if state != AvailabilityReady {
		return false, reason
	}
	if e.session != nil {
		if e.session.State != SessionEnded {
			return false, protocol.CodeSessionExists
		}
		if e.cleanup == nil || e.cleanup.State != protocol.CleanupComplete {
			return false, protocol.CodeCleanupUnconfirmed
		}
	}
	ready, why := e.appReadyLocked(now)
	if !ready {
		return false, why
	}
	if e.app.StopPending != nil || e.stopPendingLocked() {
		return false, protocol.CodeStopped
	}
	if e.app.InstanceID == "" {
		return false, protocol.CodeControlUnavailable
	}
	return true, ""
}

func (e *Engine) stopPendingLocked() bool {
	if e.stopBoundary == nil {
		return false
	}
	// A new start requires a sync from the current App instance after the stop
	// boundary was accepted and no pending intent remains.
	if !e.app.LastSync.After(e.stopBoundary.AcceptedAt) {
		return true
	}
	return e.stopSeqByApp[e.app.InstanceID] > 0 && e.app.StopPending != nil
}

// broadcastAppLocked wakes everyone waiting for a control-App change.
func (e *Engine) broadcastAppLocked() {
	close(e.appNotify)
	e.appNotify = make(chan struct{})
}

func (e *Engine) appChangeLocked() <-chan struct{} { return e.appNotify }

// StartClock starts the bounded housekeeping ticker. It expires sessions and
// observations and drives recovery checks without holding any device lock.
func (e *Engine) StartClock() {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				e.housekeeping(context.Background())
			case <-e.tickerDone:
				return
			}
		}
	}()
}

func (e *Engine) housekeeping(ctx context.Context) {
	now := time.Now()
	e.mu.Lock()
	var expired *sessionState
	if e.session != nil && e.session.State != SessionEnded && !sessionBeforeDeadline(e.session, now) {
		expired = e.revokeSessionLocked("lease_expired", now)
	}
	for _, obs := range e.observations {
		if obs.Status == ObservationReady && now.After(obs.ActionableUntil) {
			obs.Status = ObservationExpired
		}
	}
	e.pruneClosedLocked(now)
	needsRecovery := e.recovery != nil
	// An unresolved managed chain is re-probed from the bounded clock, never
	// from the request path, so a natural end does not keep admission closed.
	probeShell := e.shell != nil && !e.shell.settled() && e.work == nil &&
		!e.shellProbing && !e.shuttingDown
	if probeShell {
		e.wg.Add(1)
	}
	e.mu.Unlock()
	if expired != nil {
		e.launchCleanup(expired, "lease_expired")
	}
	if probeShell {
		go func() { defer e.wg.Done(); e.confirmUnresolvedShell(ctx) }()
	}
	if needsRecovery {
		e.driveRecovery(ctx)
	}
}

// Shutdown closes ordinary admission, revokes the original session, starts the
// independent bounded closure and waits for accepted background work. It never
// reports cleanup=complete merely because the process exits.
func (e *Engine) Shutdown(ctx context.Context) {
	e.shutdownOnce.Do(func() { close(e.tickerDone) })
	e.mu.Lock()
	e.shuttingDown = true
	var session *sessionState
	var cleanup *cleanupState
	if e.session != nil && e.session.State != SessionEnded {
		session = e.revokeSessionLocked("service_shutdown", time.Now())
		cleanup = e.beginCleanupLocked(session, "service_shutdown")
	}
	e.broadcastAppLocked()
	e.mu.Unlock()
	if session != nil && cleanup != nil {
		e.launchPreparedCleanup(session, "service_shutdown", cleanup)
	}
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	saveCtx, cancel := context.WithTimeout(context.Background(), e.cfg.LedgerTimeout)
	defer cancel()
	_ = e.persistSync(saveCtx)
}

// revokeSessionLocked ends the session, retires the ready observation and
// cancels any work. It must not perform I/O or wait.
func (e *Engine) revokeSessionLocked(reason string, now time.Time) *sessionState {
	session := e.session
	if session == nil || session.State == SessionEnded {
		return session
	}
	session.State = SessionEnded
	session.EndedAt = now
	session.Reason = reason
	if session.ExpireTimer != nil {
		session.ExpireTimer.Stop()
	}
	if e.current != nil && e.current.Status == ObservationReady {
		e.current.Status = ObservationRetired
		e.current.RetiredAt = now
	}
	e.removeCommandLocked(session.CreateCommand)
	if e.work != nil {
		e.work.Revoked = true
		if e.work.Cancel != nil {
			e.work.Cancel()
		}
	}
	return session
}

// touchSessionLocked applies a successful ordinary work lease extension. It is
// only called for valid accepted work, not for queries or rejected requests.
func (e *Engine) touchSessionLocked(now time.Time) {
	if e.session == nil || e.session.State != SessionActive || !sessionBeforeDeadline(e.session, now) {
		return
	}
	next := now.Add(e.cfg.SessionTTL)
	if next.After(e.session.HardExpiresAt) {
		next = e.session.HardExpiresAt
	}
	if next.After(e.session.ExpiresAt) {
		e.session.ExpiresAt = next
	}
}

// ObservationID allocation.
func (e *Engine) nextObservationIDLocked() string {
	e.nextObservation++
	return "O" + strconv.FormatInt(e.nextObservation, 10)
}

// Operation IDs generated by the service when the SDK did not supply one.
func (e *Engine) nextOperationIDLocked() string {
	e.nextOperation++
	return "P" + strconv.FormatInt(e.nextOperation, 10)
}

func (e *Engine) nextCommandIDLocked() string {
	// Display commands cross the App's Binder nonce boundary, so their identity
	// must be a UUID rather than a visible counter.
	return NewUUID()
}

// NewUUID returns a random RFC 4122 version 4 UUID string.
func NewUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" +
		encoded[16:20] + "-" + encoded[20:32]
}

// ValidateUUID reports whether value is a canonical lowercase UUID.
func ValidateUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				return false
			}
		}
	}
	return true
}

func (e *Engine) sessionViewLocked(session *sessionState) *protocol.Session {
	if session == nil {
		return nil
	}
	view := &protocol.Session{
		Origin:    session.Origin,
		State:     session.State,
		Mode:      session.Mode,
		Agent:     session.Agent,
		StartedAt: formatTime(session.StartedAt),
	}
	switch session.State {
	case SessionStarting, SessionActive:
		view.ExpiresAt = formatTime(session.ExpiresAt)
	case SessionEnded:
		view.EndedAt = formatTime(session.EndedAt)
		view.Reason = session.Reason
	}
	return view
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (e *Engine) cleanupViewLocked() *protocol.Cleanup {
	if e.cleanup == nil {
		return nil
	}
	return &protocol.Cleanup{
		State:     e.cleanup.State,
		Execution: e.cleanup.Execution,
		Tasks:     e.cleanup.Tasks,
		Recents:   e.cleanup.Recents,
		Display:   e.cleanup.Display,
		Error:     e.cleanup.Error,
	}
}

func (e *Engine) currentSessionViewLocked() *protocol.Session {
	if e.session == nil {
		return nil
	}
	return e.sessionViewLocked(e.session)
}

// findReferenceLocked classifies an O/P reference without side effects.
func (e *Engine) findReferenceLocked(ref string) (kind string, obs *observationState, op *operationState) {
	switch {
	case strings.HasPrefix(ref, "O"):
		return "O", e.observations[ref], nil
	case strings.HasPrefix(ref, "P"):
		return "P", nil, e.operations[ref]
	default:
		return "", nil, nil
	}
}

// observationTree returns the current committed tree of an observation.
func (o *observationState) tree() *protocol.Tree {
	if o == nil || o.Projection == nil {
		return nil
	}
	return o.Projection.Tree
}

// sessionByOriginLocked finds the current or most recently ended session.
func (e *Engine) sessionByOriginLocked(origin string) *sessionState {
	if origin == "" {
		return nil
	}
	if e.session != nil && e.session.Origin == origin {
		return e.session
	}
	if e.lastSession != nil && e.lastSession.Origin == origin {
		return e.lastSession
	}
	return nil
}

// sessionByEpochLocked finds the session that owned an observation epoch.
func (e *Engine) sessionByEpochLocked(epoch int64) *sessionState {
	if e.session != nil && e.session.Epoch == epoch {
		return e.session
	}
	if e.lastSession != nil && e.lastSession.Epoch == epoch {
		return e.lastSession
	}
	return nil
}

// addClosedLocked records one atomic closed-before-admission marker.
func (e *Engine) addClosedLocked(ref string, now time.Time) {
	if ref == "" {
		return
	}
	if _, exists := e.closed[ref]; !exists {
		e.closedOrder = append(e.closedOrder, ref)
	}
	e.closed[ref] = now
	for len(e.closedOrder) > 512 {
		oldest := e.closedOrder[0]
		e.closedOrder = e.closedOrder[1:]
		delete(e.closed, oldest)
	}
}

func (e *Engine) pruneClosedLocked(now time.Time) {
	keep := e.closedOrder[:0]
	for _, ref := range e.closedOrder {
		at, ok := e.closed[ref]
		if !ok || now.Sub(at) > e.cfg.MaxAcceptanceAge {
			delete(e.closed, ref)
			continue
		}
		keep = append(keep, ref)
	}
	e.closedOrder = keep
}

// operationView builds the status(P) projection without new side effects.
func (e *Engine) operationViewLocked(op *operationState) *protocol.Result {
	result := &protocol.Result{OperationID: op.ID, OperationState: op.State}
	if op.State == OperationFinished {
		result.Outcome = op.Outcome
		result.Phase = op.Phase
	}
	if op.Error != nil {
		result.Error = op.Error
	}
	// A finished operation replays the projection it already returned; the
	// status tool applies its own live cleanup override on top.
	result.Recovery = op.Recovery
	if session := e.sessionByOriginLocked(op.SessionOrigin); session != nil {
		result.Session = e.sessionViewLocked(session)
		result.IncludeSession = true
	}
	if (op.Kind == "end" || (op.SessionOrigin != "" && result.Session != nil &&
		result.Session.State == SessionEnded)) && e.cleanup != nil {
		result.Cleanup = e.cleanupViewLocked()
	}
	return result
}

// IsBusy reports whether the ordinary device work position is occupied.
func (e *Engine) IsBusy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.work != nil
}

var errNotFound = errors.New("not found")
