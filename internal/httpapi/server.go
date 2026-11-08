// Package httpapi is the authenticated local HTTP boundary. It performs strict
// JSON admission, maps controlled errors to HTTP status and never owns device
// or session authority.
package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/engine"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Header names of the shared ordinary contract and the pre-assigned operation.
const (
	ContractHeader  = "X-Mobile-Use-Contract"
	OperationHeader = "X-Mobile-Use-Operation"
)

// Config is the local listener and identity configuration.
type Config struct {
	Host              string
	Port              int
	AgentToken        string
	ControlToken      string
	AgentName         string
	ServiceInstanceID string
	ReleaseVersion    string
}

// Server wraps the engine behind the authenticated local HTTP boundary.
type Server struct {
	cfg      Config
	engine   *engine.Engine
	log      *eventlog.Logger
	delivery *deliveryLimiter
	http     *http.Server
	handler  http.Handler
}

// New creates the HTTP server. The caller starts ListenAndServe.
func New(cfg Config, eng *engine.Engine, log *eventlog.Logger) *Server {
	s := &Server{cfg: cfg, engine: eng, log: log, delivery: &deliveryLimiter{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/api/start", s.agent(s.start))
	mux.HandleFunc("/api/status", s.agent(s.status))
	mux.HandleFunc("/api/observe", s.agent(s.observe))
	mux.HandleFunc("/api/act", s.agent(s.act))
	mux.HandleFunc("/api/end", s.agent(s.end))
	mux.HandleFunc("/api/inspect", s.agent(s.inspect))
	mux.HandleFunc("/api/keepalive", s.agent(s.keepalive))
	mux.HandleFunc("/api/diagnose", s.agent(s.diagnose))
	mux.HandleFunc("/api/notify", s.agent(s.notify))
	mux.HandleFunc("/api/apps", s.agent(s.apps))
	mux.HandleFunc("/api/open_app", s.agent(s.openApp))
	mux.HandleFunc("/api/shell", s.agent(s.shell))
	mux.HandleFunc("/v1/control/sync", s.control(s.sync))
	mux.HandleFunc("/v1/control/stop", s.control(s.stop))
	s.handler = mux
	s.http = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	return s
}

// Handler exposes the routes for tests.
func (s *Server) Handler() http.Handler { return s.handler }

// ListenAndServe binds the configured loopback address and serves until closed.
func (s *Server) ListenAndServe() error {
	address := net.JoinHostPort(s.cfg.Host, fmt.Sprintf("%d", s.cfg.Port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	s.http.Addr = address
	err = s.http.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops accepting connections and waits for in-flight handlers.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, &protocol.Health{
		Status: "ok", ContractVersion: protocol.ContractVersion,
		ServiceInstanceID: s.cfg.ServiceInstanceID,
		ReleaseVersion:    s.cfg.ReleaseVersion,
	})
}

type agentHandler func(w http.ResponseWriter, r *http.Request, p string)

// agent wraps an ordinary tool route: POST only, bearer agent identity, exact
// contract version and an optional pre-assigned P.
func (s *Server) agent(next agentHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, protocol.NewError(http.StatusMethodNotAllowed, protocol.CodeInvalidRequest,
				"仅接受 POST"))
			return
		}
		if !s.authorized(r, s.cfg.AgentToken) {
			writeAPIError(w, protocol.NewError(http.StatusUnauthorized, protocol.CodeUnauthorized,
				"认证失败"))
			return
		}
		if r.Header.Get(ContractHeader) != protocol.ContractVersion {
			writeAPIError(w, protocol.NewError(http.StatusConflict, protocol.CodeContractMismatch,
				"普通契约版本不匹配").WithNext("update"))
			return
		}
		p := r.Header.Get(OperationHeader)
		next(w, r, p)
	}
}

type controlHandler func(w http.ResponseWriter, r *http.Request)

// control wraps a control-App route: POST only and bearer control identity.
func (s *Server) control(next controlHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, protocol.NewError(http.StatusMethodNotAllowed, protocol.CodeInvalidRequest,
				"仅接受 POST"))
			return
		}
		if !s.authorized(r, s.cfg.ControlToken) {
			writeAPIError(w, protocol.NewError(http.StatusUnauthorized, protocol.CodeUnauthorized,
				"认证失败"))
			return
		}
		next(w, r)
	}
}

func (s *Server) authorized(r *http.Request, token string) bool {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	provided := header[len(prefix):]
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}

func (s *Server) start(w http.ResponseWriter, r *http.Request, p string) {
	body, ok := readBody(w, r, 16*1024)
	if !ok {
		return
	}
	if _, apiErr := protocol.DecodeStart(body); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	release, ok := s.delivery.acquire(deliveryReserveTree)
	if !ok {
		writeAPIError(w, protocol.NewError(503, protocol.CodeDeliveryUnavailable,
			"结果交付容量不足，请在当前结果交付后再试"))
		return
	}
	defer release()
	result, apiErr := s.engine.Start(r.Context(), s.cfg.AgentName, p)
	s.finish(w, result, apiErr)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request, p string) {
	body, ok := readBody(w, r, 16*1024)
	if !ok {
		return
	}
	req, apiErr := protocol.DecodeStatus(body)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	result, apiErr := s.engine.Status(s.cfg.AgentName, req.Reference)
	s.finish(w, result, apiErr)
}

func (s *Server) observe(w http.ResponseWriter, r *http.Request, p string) {
	body, ok := readBody(w, r, 32*1024)
	if !ok {
		return
	}
	req, apiErr := protocol.DecodeObserve(body)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	release, ok := s.delivery.acquire(deliveryReserveTree)
	if !ok {
		writeAPIError(w, protocol.NewError(503, protocol.CodeDeliveryUnavailable,
			"结果交付容量不足，请在当前结果交付后再试"))
		return
	}
	defer release()
	result, apiErr := s.engine.Observe(r.Context(), s.cfg.AgentName, p, req)
	s.finish(w, result, apiErr)
}

func (s *Server) act(w http.ResponseWriter, r *http.Request, p string) {
	body, ok := readBody(w, r, 64*1024)
	if !ok {
		return
	}
	req, apiErr := protocol.DecodeAct(body)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	release, ok := s.delivery.acquire(deliveryReserveTree)
	if !ok {
		writeAPIError(w, protocol.NewError(503, protocol.CodeDeliveryUnavailable,
			"结果交付容量不足，请在当前结果交付后再试"))
		return
	}
	defer release()
	result, apiErr := s.engine.Act(r.Context(), s.cfg.AgentName, p, req)
	s.finish(w, result, apiErr)
}

func (s *Server) end(w http.ResponseWriter, r *http.Request, p string) {
	body, ok := readBody(w, r, 16*1024)
	if !ok {
		return
	}
	req, apiErr := protocol.DecodeEnd(body)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	result, apiErr := s.engine.End(s.cfg.AgentName, p, req.Reference)
	s.finish(w, result, apiErr)
}

func (s *Server) inspect(w http.ResponseWriter, r *http.Request, p string) {
	body, ok := readBody(w, r, 8*1024)
	if !ok {
		return
	}
	req, apiErr := protocol.DecodeInspect(body)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	release, ok := s.delivery.acquire(deliveryReserveTree)
	if !ok {
		writeAPIError(w, protocol.NewError(503, protocol.CodeDeliveryUnavailable,
			"结果交付容量不足，请在当前结果交付后再试"))
		return
	}
	defer release()
	result, apiErr := s.engine.Inspect(s.cfg.AgentName, req)
	s.finish(w, result, apiErr)
}

func (s *Server) keepalive(w http.ResponseWriter, r *http.Request, p string) {
	body, ok := readBody(w, r, 8*1024)
	if !ok {
		return
	}
	req, apiErr := protocol.DecodeKeepalive(body)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	result, apiErr := s.engine.Keepalive(s.cfg.AgentName, p, req)
	s.finish(w, result, apiErr)
}

func (s *Server) diagnose(w http.ResponseWriter, r *http.Request, p string) {
	body, ok := readBody(w, r, 8*1024)
	if !ok {
		return
	}
	req, apiErr := protocol.DecodeDiagnose(body)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	release, ok := s.delivery.acquire(deliveryReserveDiag)
	if !ok {
		writeAPIError(w, protocol.NewError(503, protocol.CodeDeliveryUnavailable,
			"结果交付容量不足，请在当前结果交付后再试"))
		return
	}
	defer release()
	result, apiErr := s.engine.Diagnose(s.cfg.AgentName, req)
	s.finish(w, result, apiErr)
}

func (s *Server) notify(w http.ResponseWriter, r *http.Request, p string) {
	body, ok := readBody(w, r, 16*1024)
	if !ok {
		return
	}
	req, apiErr := protocol.DecodeNotify(body)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	result, apiErr := s.engine.Notify(s.cfg.AgentName, p, req)
	s.finish(w, result, apiErr)
}

func (s *Server) apps(w http.ResponseWriter, r *http.Request, p string) {
	body, ok := readBody(w, r, 8*1024)
	if !ok {
		return
	}
	req, apiErr := protocol.DecodeApps(body)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	release, ok := s.delivery.acquire(deliveryReserveApps)
	if !ok {
		writeAPIError(w, protocol.NewError(503, protocol.CodeDeliveryUnavailable,
			"结果交付容量不足，请在当前结果交付后再试"))
		return
	}
	defer release()
	result, apiErr := s.engine.Apps(r.Context(), s.cfg.AgentName, p, req)
	s.finish(w, result, apiErr)
}

func (s *Server) openApp(w http.ResponseWriter, r *http.Request, p string) {
	body, ok := readBody(w, r, 8*1024)
	if !ok {
		return
	}
	req, apiErr := protocol.DecodeOpenApp(body)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	release, ok := s.delivery.acquire(deliveryReserveTree)
	if !ok {
		writeAPIError(w, protocol.NewError(503, protocol.CodeDeliveryUnavailable,
			"结果交付容量不足，请在当前结果交付后再试"))
		return
	}
	defer release()
	result, apiErr := s.engine.OpenApp(r.Context(), s.cfg.AgentName, p, req)
	s.finish(w, result, apiErr)
}

func (s *Server) shell(w http.ResponseWriter, r *http.Request, p string) {
	body, ok := readBody(w, r, 32*1024)
	if !ok {
		return
	}
	req, apiErr := protocol.DecodeShell(body)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	release, ok := s.delivery.acquire(deliveryReserveShell)
	if !ok {
		writeAPIError(w, protocol.NewError(503, protocol.CodeDeliveryUnavailable,
			"结果交付容量不足，请在当前结果交付后再试"))
		return
	}
	defer release()
	result, apiErr := s.engine.Shell(r.Context(), s.cfg.AgentName, p, req)
	s.finish(w, result, apiErr)
}

func (s *Server) sync(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r, 32*1024)
	if !ok {
		return
	}
	var req protocol.AppSyncRequest
	if err := protocol.Decode(body, &req); err != nil {
		writeAPIError(w, protocol.Invalid("控制同步请求不符合契约"))
		return
	}
	response, apiErr := s.engine.SyncApp(r.Context(), &req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r, 8*1024)
	if !ok {
		return
	}
	var req protocol.ControlStopRequest
	if err := protocol.Decode(body, &req); err != nil {
		writeAPIError(w, protocol.Invalid("停止请求不符合最小载体契约"))
		return
	}
	response, apiErr := s.engine.Stop(r.Context(), &req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// finish writes the engine result and derives the layered HTTP status without
// rewriting any known stage fact.
func (s *Server) finish(w http.ResponseWriter, result *protocol.Result, apiErr *protocol.APIError) {
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if result == nil {
		writeAPIError(w, protocol.Internal())
		return
	}
	status := http.StatusOK
	if result.Error != nil {
		code := result.Error.Code
		status = protocol.Status(code)
		if status == http.StatusInternalServerError {
			status = http.StatusConflict
		}
	} else if result.Observation != nil && result.Observation.Status == "failed" {
		status = http.StatusServiceUnavailable
	}
	writeJSONDelivery(w, status, result)
}

func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		writeAPIError(w, protocol.Invalid("请求体读取失败"))
		return nil, false
	}
	if int64(len(body)) > limit {
		writeAPIError(w, protocol.Invalid("请求体超过大小上限"))
		return nil, false
	}
	return body, true
}

func writeAPIError(w http.ResponseWriter, apiErr *protocol.APIError) {
	status := apiErr.HTTPStatus
	if status == 0 {
		status = protocol.Status(apiErr.Code)
	}
	result := &protocol.Result{Error: apiErr.Public(), Next: apiErr.Next}
	if apiErr.Outcome != "" {
		result.Outcome = apiErr.Outcome
	}
	writeJSON(w, status, result)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := protocol.JSON(value)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":"internal_error","message":"服务内部错误"}}`)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
