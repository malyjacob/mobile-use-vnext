package protocol

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Tool request shapes. Each tool admits exactly the members listed by its
// presence struct; every optional member is omitted from the wire when not
// supplied (never encoded as an explicit null).

// Application lifecycle kinds of the single app tool. Every kind is a closed
// branch with its own required and admitted members.
const (
	AppKindFind    = "find"
	AppKindList    = "list"
	AppKindOpen    = "open"
	AppKindClaim   = "claim"
	AppKindHandoff = "handoff"
	AppKindClose   = "close"
	AppKindKill    = "kill"
)

// AppKinds is the closed kind vocabulary in catalog order.
var AppKinds = []string{
	AppKindFind, AppKindList, AppKindOpen, AppKindClaim, AppKindHandoff,
	AppKindClose, AppKindKill,
}

// List scopes. A primary session lists the same primary display either way;
// scope never grants task ownership.
const (
	AppScopeSession = "session"
	AppScopePrimary = "primary"
)

// Task ownership values published by list and by the mutating kinds.
const (
	TaskOwnershipOwned    = "owned"
	TaskOwnershipBorrowed = "borrowed"
	TaskOwnershipShared   = "shared"
)

// Handoff foreground-restore facts.
const (
	ForegroundRestoreNotRequested = "not_requested"
	ForegroundRestoreConfirmed    = "confirmed"
	ForegroundRestoreUnconfirmed  = "unconfirmed"
)

// AppTarget selects exactly one live task: a task id or an explicit package.
type AppTarget struct {
	TaskID  int
	App     string
	UseTask bool
}

// AppRequest is app(kind, ...). Only the branch members of Kind are populated;
// the wire decoder rejects every other member before the engine sees it.
type AppRequest struct {
	Kind string

	// find
	Query    string
	HasQuery bool

	// list
	Reference string
	Scope     string
	FilterApp string
	HasFilter bool

	// open, claim, handoff, close, kill; list also uses Reference.
	Target *AppTarget

	RestoreForeground bool
	AllowBorrowed     bool
	AllowStopPlayback bool
}

// RequiresObservation reports whether the kind consumes a current ready O.
func (r *AppRequest) RequiresObservation() bool {
	switch r.Kind {
	case AppKindOpen, AppKindClaim, AppKindHandoff, AppKindClose, AppKindKill:
		return true
	}
	return false
}

// IsQuery reports whether the kind is a read-only query without an outcome.
func (r *AppRequest) IsQuery() bool {
	return r.Kind == AppKindFind || r.Kind == AppKindList
}

// MutatesTasks reports whether the kind dispatches a task or process change.
func (r *AppRequest) MutatesTasks() bool {
	switch r.Kind {
	case AppKindOpen, AppKindClaim, AppKindHandoff, AppKindClose:
		return true
	}
	return false
}

// NotifyRequest is notify(text).
type NotifyRequest struct {
	Text string
}

// ShellRequest is shell(reference, command, root=false).
type ShellRequest struct {
	Reference string
	Command   string
	Root      bool
}

// InspectRequest is inspect(reference, node=None, query=None).
type InspectRequest struct {
	Reference string
	Node      string
	Query     string
	HasNode   bool
	HasQuery  bool
}

// KeepaliveRequest is keepalive(reference, duration_minutes=2). Presence is
// preserved so an omitted duration can never be confused with an explicit zero.
type KeepaliveRequest struct {
	Reference          string
	DurationMinutes    int
	HasDurationMinutes bool
}

// EffectiveDurationMinutes applies the shared default and range rules without
// changing presence. Direct engine calls and wire encoders use the same check.
func (r *KeepaliveRequest) EffectiveDurationMinutes() (int, *APIError) {
	if !r.HasDurationMinutes {
		return 2, nil
	}
	if r.DurationMinutes < 1 || r.DurationMinutes > 120 {
		return 0, Invalid("duration_minutes 必须是 1～120 的整数")
	}
	return r.DurationMinutes, nil
}

// DiagnoseRequest is diagnose(reference=None).
type DiagnoseRequest struct {
	Reference string
	HasRef    bool
}

type rawAppTarget struct {
	TaskID jsontext.Value `json:"task_id"`
	App    jsontext.Value `json:"app"`
}

// DecodeApp admits exactly one strict app kind branch. Every branch declares its
// own member set: a member belonging to another kind is rejected before any
// session check, so a query can never inherit a mutation's admission path.
func DecodeApp(body []byte) (*AppRequest, *APIError) {
	var presence struct {
		Kind              jsontext.Value `json:"kind"`
		Query             jsontext.Value `json:"query"`
		Reference         jsontext.Value `json:"reference"`
		Scope             jsontext.Value `json:"scope"`
		App               jsontext.Value `json:"app"`
		Target            jsontext.Value `json:"target"`
		RestoreForeground jsontext.Value `json:"restore_foreground"`
		AllowBorrowed     jsontext.Value `json:"allow_borrowed"`
		AllowStopPlayback jsontext.Value `json:"allow_stop_playback"`
	}
	var wire struct {
		Kind              string        `json:"kind"`
		Query             string        `json:"query"`
		Reference         string        `json:"reference"`
		Scope             string        `json:"scope"`
		App               string        `json:"app"`
		Target            *rawAppTarget `json:"target"`
		RestoreForeground bool          `json:"restore_foreground"`
		AllowBorrowed     bool          `json:"allow_borrowed"`
		AllowStopPlayback bool          `json:"allow_stop_playback"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.Kind) == 0 || string(presence.Kind) == "null" {
		return nil, Invalid("app 需要 kind")
	}
	kind := wire.Kind
	members := []struct {
		name string
		raw  jsontext.Value
	}{
		{"query", presence.Query},
		{"reference", presence.Reference},
		{"scope", presence.Scope},
		{"app", presence.App},
		{"target", presence.Target},
		{"restore_foreground", presence.RestoreForeground},
		{"allow_borrowed", presence.AllowBorrowed},
		{"allow_stop_playback", presence.AllowStopPlayback},
	}
	allowed := appKindMembers(kind)
	if allowed == nil {
		return nil, Invalid("不支持的 app kind").WithDetail("allowed_kinds", AppKinds)
	}
	for _, member := range members {
		if len(member.raw) == 0 {
			continue
		}
		if !allowed[member.name] {
			return nil, Invalid(kind+" 不接受该成员").WithDetail("field", member.name)
		}
		if string(member.raw) == "null" {
			return nil, Invalid("参数不接受显式 null；省略该字段使用默认值").
				WithDetail("field", member.name)
		}
	}
	req := &AppRequest{Kind: kind}
	switch kind {
	case AppKindFind:
		req.Query, req.HasQuery = wire.Query, len(presence.Query) > 0
		if utf8.RuneCountInString(req.Query) > MaxQueryRunes {
			return nil, Invalid("query 超过长度上限").WithDetail("limit", MaxQueryRunes)
		}
		return req, nil
	case AppKindList:
		req.Reference = wire.Reference
		if err := checkReference(req.Reference); err != nil {
			return nil, err
		}
		req.Scope = wire.Scope
		if req.Scope == "" {
			req.Scope = AppScopeSession
		}
		if req.Scope != AppScopeSession && req.Scope != AppScopePrimary {
			return nil, Invalid("scope 必须是 session 或 primary").
				WithDetail("allowed_scopes", []string{AppScopeSession, AppScopePrimary})
		}
		req.FilterApp, req.HasFilter = wire.App, len(presence.App) > 0
		if req.HasFilter && !ValidPackageName(req.FilterApp) {
			return nil, Invalid("app 必须是明确的应用包名")
		}
		return req, nil
	case AppKindOpen:
		req.Reference = wire.Reference
		if err := checkReference(req.Reference); err != nil {
			return nil, err
		}
		if wire.Target == nil {
			return nil, Invalid("open 需要 target")
		}
		target, apiErr := parseAppTarget(kind, wire.Target)
		if apiErr != nil {
			return nil, apiErr
		}
		req.Target = target
		return req, nil
	case AppKindClaim, AppKindHandoff, AppKindClose, AppKindKill:
		req.Reference = wire.Reference
		if err := checkReference(req.Reference); err != nil {
			return nil, err
		}
		if wire.Target == nil {
			return nil, Invalid(kind + " 需要 target")
		}
		target, apiErr := parseAppTarget(kind, wire.Target)
		if apiErr != nil {
			return nil, apiErr
		}
		req.Target = target
		req.RestoreForeground = wire.RestoreForeground
		req.AllowBorrowed = wire.AllowBorrowed
		req.AllowStopPlayback = wire.AllowStopPlayback
		return req, nil
	}
	return nil, Invalid("不支持的 app kind").WithDetail("allowed_kinds", AppKinds)
}

// appKindMembers returns the closed member set of one kind, excluding kind.
func appKindMembers(kind string) map[string]bool {
	switch kind {
	case AppKindFind:
		return map[string]bool{"query": true}
	case AppKindList:
		return map[string]bool{"reference": true, "scope": true, "app": true}
	case AppKindOpen:
		return map[string]bool{"reference": true, "target": true}
	case AppKindClaim:
		return map[string]bool{"reference": true, "target": true}
	case AppKindHandoff:
		return map[string]bool{
			"reference": true, "target": true, "restore_foreground": true,
		}
	case AppKindClose:
		return map[string]bool{
			"reference": true, "target": true, "allow_borrowed": true,
		}
	case AppKindKill:
		return map[string]bool{
			"reference": true, "target": true, "allow_stop_playback": true,
		}
	}
	return nil
}

// parseAppTarget admits exactly one selector. kill only accepts a task id;
// open only accepts a package.
func parseAppTarget(kind string, raw *rawAppTarget) (*AppTarget, *APIError) {
	hasTask := len(raw.TaskID) > 0
	hasApp := len(raw.App) > 0
	switch {
	case hasTask && hasApp:
		return nil, Invalid("target 的 task_id 与 app 互斥")
	case !hasTask && !hasApp:
		return nil, Invalid("target 需要 task_id 或 app")
	case hasTask:
		if kind == AppKindOpen {
			return nil, Invalid("open 只接受 target.app")
		}
		var taskID int
		if err := json.Unmarshal(raw.TaskID, &taskID); err != nil {
			return nil, Invalid("target.task_id 必须是整数")
		}
		if taskID <= 0 {
			return nil, Invalid("target.task_id 必须是正整数")
		}
		return &AppTarget{TaskID: taskID, UseTask: true}, nil
	}
	if kind == AppKindKill {
		return nil, Invalid("kill 只接受 target.task_id；包名需先由 list 解析")
	}
	var app string
	if err := json.Unmarshal(raw.App, &app); err != nil {
		return nil, Invalid("target.app 必须是字符串")
	}
	if !ValidPackageName(app) {
		return nil, Invalid("target.app 必须是明确的应用包名")
	}
	return &AppTarget{App: app}, nil
}

// DecodeNotify admits notify(text). The text must be present and non-empty;
// empty strings are not a message and are rejected instead of silently saved.
func DecodeNotify(body []byte) (*NotifyRequest, *APIError) {
	var presence struct {
		Text jsontext.Value `json:"text"`
	}
	var wire struct {
		Text string `json:"text"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.Text) == 0 || string(presence.Text) == "null" {
		return nil, Invalid("notify 需要非 null 的 text")
	}
	if wire.Text == "" {
		return nil, Invalid("notify 的 text 不能为空")
	}
	if utf8.RuneCountInString(wire.Text) > MaxTextRunes {
		return nil, Invalid("text 超过长度上限").WithDetail("limit", MaxTextRunes)
	}
	return &NotifyRequest{Text: wire.Text}, nil
}

// DecodeShell admits shell(reference, command, root).
func DecodeShell(body []byte) (*ShellRequest, *APIError) {
	var presence struct {
		Reference jsontext.Value `json:"reference"`
		Command   jsontext.Value `json:"command"`
		Root      jsontext.Value `json:"root"`
	}
	var wire struct {
		Reference string `json:"reference"`
		Command   string `json:"command"`
		Root      bool   `json:"root"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.Reference) == 0 || string(presence.Reference) == "null" {
		return nil, Invalid("shell 需要 reference")
	}
	if len(presence.Command) == 0 || string(presence.Command) == "null" {
		return nil, Invalid("shell 需要非 null 的 command")
	}
	if err := checkNulls(map[string]jsontext.Value{"root": presence.Root}); err != nil {
		return nil, err
	}
	if err := checkReference(wire.Reference); err != nil {
		return nil, err
	}
	if wire.Command == "" {
		return nil, Invalid("shell 的 command 不能为空")
	}
	if utf8.RuneCountInString(wire.Command) > MaxCommandRunes {
		return nil, Invalid("command 超过长度上限").WithDetail("limit", MaxCommandRunes)
	}
	if strings.ContainsRune(wire.Command, '\x00') {
		return nil, Invalid("command 不能包含 NUL 字符")
	}
	return &ShellRequest{Reference: wire.Reference, Command: wire.Command, Root: wire.Root}, nil
}

// DecodeInspect admits inspect(reference, node, query). node and query
// are mutually exclusive and both are literals, never selector languages.
func DecodeInspect(body []byte) (*InspectRequest, *APIError) {
	var presence struct {
		Reference jsontext.Value `json:"reference"`
		Node      jsontext.Value `json:"node"`
		Query     jsontext.Value `json:"query"`
	}
	var wire struct {
		Reference string `json:"reference"`
		Node      string `json:"node"`
		Query     string `json:"query"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.Reference) == 0 || string(presence.Reference) == "null" {
		return nil, Invalid("inspect 需要 reference")
	}
	if err := checkReference(wire.Reference); err != nil {
		return nil, err
	}
	hasNode := len(presence.Node) > 0
	hasQuery := len(presence.Query) > 0
	if hasNode && string(presence.Node) == "null" {
		return nil, Invalid("node 不接受 null")
	}
	if hasQuery && string(presence.Query) == "null" {
		return nil, Invalid("query 不接受 null")
	}
	if hasNode && hasQuery {
		return nil, Invalid("node 与 query 不能同时出现")
	}
	if hasNode && wire.Node == "" {
		return nil, Invalid("node 不能为空")
	}
	if hasQuery && utf8.RuneCountInString(wire.Query) > MaxQueryRunes {
		return nil, Invalid("query 超过长度上限").WithDetail("limit", MaxQueryRunes)
	}
	return &InspectRequest{
		Reference: wire.Reference,
		Node:      wire.Node, Query: wire.Query,
		HasNode: hasNode, HasQuery: hasQuery,
	}, nil
}

// DecodeKeepalive admits keepalive(reference, duration_minutes).
func DecodeKeepalive(body []byte) (*KeepaliveRequest, *APIError) {
	var presence struct {
		Reference       jsontext.Value `json:"reference"`
		DurationMinutes jsontext.Value `json:"duration_minutes"`
	}
	var wire struct {
		Reference       string `json:"reference"`
		DurationMinutes int    `json:"duration_minutes"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.Reference) == 0 || string(presence.Reference) == "null" {
		return nil, Invalid("keepalive 需要 reference")
	}
	if err := checkReference(wire.Reference); err != nil {
		return nil, err
	}
	if err := checkNulls(map[string]jsontext.Value{"duration_minutes": presence.DurationMinutes}); err != nil {
		return nil, err
	}
	req := &KeepaliveRequest{
		Reference: wire.Reference, DurationMinutes: wire.DurationMinutes,
		HasDurationMinutes: len(presence.DurationMinutes) > 0,
	}
	if _, apiErr := req.EffectiveDurationMinutes(); apiErr != nil {
		return nil, apiErr
	}
	return req, nil
}

// DecodeDiagnose admits diagnose(reference).
func DecodeDiagnose(body []byte) (*DiagnoseRequest, *APIError) {
	var presence struct {
		Reference jsontext.Value `json:"reference"`
	}
	var wire struct {
		Reference string `json:"reference"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	hasRef := len(presence.Reference) > 0
	if hasRef && string(presence.Reference) == "null" {
		return nil, Invalid("reference 不接受 null；省略该字段查看自身近期记录")
	}
	if hasRef {
		if err := checkReference(wire.Reference); err != nil {
			return nil, err
		}
	}
	return &DiagnoseRequest{Reference: wire.Reference, HasRef: hasRef}, nil
}

// ValidPackageName reports whether value is a syntactically valid Android
// package name. It grants no visibility: the device still decides whether the
// package exists and is launchable.
func ValidPackageName(value string) bool {
	if value == "" || len(value) > 255 {
		return false
	}
	for _, segment := range strings.Split(value, ".") {
		if segment == "" {
			return false
		}
		for i := 0; i < len(segment); i++ {
			c := segment[i]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '_' {
				continue
			}
			return false
		}
	}
	return true
}

// AppItem is one discovered launchable application of the find kind. Name is
// the localized launcher label and is user-facing content; it must never be
// logged.
type AppItem struct {
	Name    string
	Package string
}

// AppTaskItem is one live task of the list kind. OriginDisplay is present only
// when the session still holds an original-ownership record for the task.
type AppTaskItem struct {
	TaskID        int
	Package       string
	Ownership     string
	OriginDisplay *int
}

// AppCloseTask is one frozen close target and its per-item facts. Outcome is
// executed / not_executed / uncertain. RecentsCleaned is present only when the
// item actually reached an independent recents verification; HasRecentsCleaned
// distinguishes an omitted member from an explicit null.
type AppCloseTask struct {
	TaskID            int
	Outcome           string
	RecentsCleaned    *bool
	HasRecentsCleaned bool
	Error             *Error
}

// AppView is the single app result object discriminated by kind. Only members
// that this call has proven appear; unknown scalars are omitted rather than
// encoded as zero, and the two declared nullable members (state_preserved,
// restarted) keep an explicit null.
type AppView struct {
	Kind string

	// find and list
	Items      []AppItem
	TaskItems  []AppTaskItem
	HasItems   bool
	Total      int
	HasTotal   bool
	Truncated  *bool
	CapturedAt string
	// list
	DisplayID  *int
	Foreground *Foreground

	// open / claim / handoff
	Package           string
	TaskID            *int
	Ownership         string
	Reused            *bool
	StatePreserved    *bool
	HasStatePreserved bool
	SourceDisplayID   *int
	ForegroundRestore string

	// close
	Tasks    []AppCloseTask
	HasTasks bool
	Complete *bool

	// kill
	SourceTaskID     *int
	TargetPIDs       []int
	KilledPIDs       []int
	RemainingPIDs    []int
	HasTargetPIDs    bool
	HasKilledPIDs    bool
	HasRemainingPIDs bool
	Restarted        *bool
	HasRestarted     bool
}

// MarshalJSONTo writes the kind-specific member set in its fixed public order.
func (a *AppView) MarshalJSONTo(enc *jsontext.Encoder) error {
	if a == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := a.writeString(enc, "kind", a.Kind); err != nil {
		return err
	}
	switch a.Kind {
	case AppKindFind:
		if err := a.writeFind(enc); err != nil {
			return err
		}
	case AppKindList:
		if err := a.writeList(enc); err != nil {
			return err
		}
	case AppKindOpen, AppKindClaim, AppKindHandoff:
		if err := a.writeOpenLike(enc); err != nil {
			return err
		}
	case AppKindClose:
		if err := a.writeClose(enc); err != nil {
			return err
		}
	case AppKindKill:
		if err := a.writeKill(enc); err != nil {
			return err
		}
	default:
		return writeNilKind(enc)
	}
	return enc.WriteToken(jsontext.EndObject)
}

func writeNilKind(enc *jsontext.Encoder) error {
	// An unknown kind must never be encoded as an empty object; the caller
	// treats the encoding failure as an internal error.
	return errUnknownAppKind
}

// errUnknownAppKind reports an internal programming error.
var errUnknownAppKind = rejectKind("app kind")

func (a *AppView) writeString(enc *jsontext.Encoder, name, value string) error {
	if err := writeName(enc, name); err != nil {
		return err
	}
	return writeString(enc, value)
}

func (a *AppView) writeIntField(enc *jsontext.Encoder, name string, value int) error {
	if err := writeName(enc, name); err != nil {
		return err
	}
	return writeInt(enc, value)
}

func (a *AppView) writeIntPtr(enc *jsontext.Encoder, name string, value *int) error {
	if err := writeName(enc, name); err != nil {
		return err
	}
	return writeInt(enc, *value)
}

func (a *AppView) writeBoolField(enc *jsontext.Encoder, name string, value bool) error {
	if err := writeName(enc, name); err != nil {
		return err
	}
	return writeBool(enc, value)
}

func (a *AppView) writeNullableBool(enc *jsontext.Encoder, name string, value *bool) error {
	if err := writeName(enc, name); err != nil {
		return err
	}
	if value == nil {
		return writeNull(enc)
	}
	return writeBool(enc, *value)
}

func (a *AppView) writeFind(enc *jsontext.Encoder) error {
	if a.hasItems() {
		if err := writeName(enc, "items"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for _, item := range a.Items {
			if err := enc.WriteToken(jsontext.BeginObject); err != nil {
				return err
			}
			if err := a.writeString(enc, "name", item.Name); err != nil {
				return err
			}
			if err := a.writeString(enc, "package", item.Package); err != nil {
				return err
			}
			if err := enc.WriteToken(jsontext.EndObject); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return err
		}
	}
	if a.hasTotal() {
		if err := a.writeIntField(enc, "total", a.Total); err != nil {
			return err
		}
	}
	return a.writeTail(enc)
}

// hasItems reports whether this view carries a collection. A non-nil slice is
// evidence of a successful read even when the explicit presence flag is unset.
func (a *AppView) hasItems() bool {
	if a.HasItems {
		return true
	}
	if a.Kind == AppKindList {
		return a.TaskItems != nil
	}
	return a.Items != nil
}

// hasTotal reports whether the pre-budget match count is known.
func (a *AppView) hasTotal() bool {
	return a.HasTotal || a.Total != 0 || a.Truncated != nil
}

func (a *AppView) writeList(enc *jsontext.Encoder) error {
	if a.DisplayID != nil {
		if err := a.writeIntPtr(enc, "display_id", a.DisplayID); err != nil {
			return err
		}
	}
	if a.hasItems() {
		if err := writeName(enc, "items"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for _, item := range a.TaskItems {
			if err := enc.WriteToken(jsontext.BeginObject); err != nil {
				return err
			}
			if err := a.writeIntField(enc, "task_id", item.TaskID); err != nil {
				return err
			}
			if err := a.writeString(enc, "package", item.Package); err != nil {
				return err
			}
			if err := a.writeString(enc, "ownership", item.Ownership); err != nil {
				return err
			}
			if item.OriginDisplay != nil {
				if err := a.writeIntPtr(enc, "origin_display", item.OriginDisplay); err != nil {
					return err
				}
			}
			if err := enc.WriteToken(jsontext.EndObject); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return err
		}
	}
	if a.hasTotal() {
		if err := a.writeIntField(enc, "total", a.Total); err != nil {
			return err
		}
	}
	if err := a.writeTail(enc); err != nil {
		return err
	}
	if a.Foreground != nil {
		if err := writeName(enc, "foreground"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, a.Foreground); err != nil {
			return err
		}
	}
	return nil
}

// writeTail writes truncated and captured_at, the shared find/list tail.
func (a *AppView) writeTail(enc *jsontext.Encoder) error {
	if a.Truncated != nil {
		if err := a.writeBoolField(enc, "truncated", *a.Truncated); err != nil {
			return err
		}
	}
	if a.CapturedAt != "" {
		return a.writeString(enc, "captured_at", a.CapturedAt)
	}
	return nil
}

func (a *AppView) writeOpenLike(enc *jsontext.Encoder) error {
	if a.Package != "" {
		if err := a.writeString(enc, "package", a.Package); err != nil {
			return err
		}
	}
	if a.TaskID != nil {
		if err := a.writeIntPtr(enc, "task_id", a.TaskID); err != nil {
			return err
		}
	}
	if a.Kind != AppKindOpen && a.SourceDisplayID != nil {
		if err := a.writeIntPtr(enc, "source_display_id", a.SourceDisplayID); err != nil {
			return err
		}
	}
	if a.DisplayID != nil {
		if err := a.writeIntPtr(enc, "display_id", a.DisplayID); err != nil {
			return err
		}
	}
	if a.Ownership != "" {
		if err := a.writeString(enc, "ownership", a.Ownership); err != nil {
			return err
		}
	}
	if a.Kind == AppKindOpen && a.Reused != nil {
		if err := a.writeBoolField(enc, "reused", *a.Reused); err != nil {
			return err
		}
	}
	if a.HasStatePreserved {
		if err := a.writeNullableBool(enc, "state_preserved", a.StatePreserved); err != nil {
			return err
		}
	}
	if a.Kind == AppKindHandoff && a.ForegroundRestore != "" {
		return a.writeString(enc, "foreground_restore", a.ForegroundRestore)
	}
	return nil
}

func (a *AppView) writeClose(enc *jsontext.Encoder) error {
	if a.DisplayID != nil {
		if err := a.writeIntPtr(enc, "display_id", a.DisplayID); err != nil {
			return err
		}
	}
	if a.HasTasks {
		if err := writeName(enc, "tasks"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for _, task := range a.Tasks {
			if err := enc.WriteToken(jsontext.BeginObject); err != nil {
				return err
			}
			if err := a.writeIntField(enc, "task_id", task.TaskID); err != nil {
				return err
			}
			if err := a.writeString(enc, "outcome", task.Outcome); err != nil {
				return err
			}
			if task.HasRecentsCleaned {
				if err := a.writeNullableBool(enc, "recents_cleaned", task.RecentsCleaned); err != nil {
					return err
				}
			}
			if task.Error != nil {
				if err := writeName(enc, "error"); err != nil {
					return err
				}
				if err := MarshalJSONTo(enc, task.Error); err != nil {
					return err
				}
			}
			if err := enc.WriteToken(jsontext.EndObject); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return err
		}
	}
	if a.Complete != nil {
		return a.writeBoolField(enc, "complete", *a.Complete)
	}
	return nil
}

func (a *AppView) writeKill(enc *jsontext.Encoder) error {
	if a.Package != "" {
		if err := a.writeString(enc, "package", a.Package); err != nil {
			return err
		}
	}
	if a.SourceTaskID != nil {
		if err := a.writeIntPtr(enc, "source_task_id", a.SourceTaskID); err != nil {
			return err
		}
	}
	if a.DisplayID != nil {
		if err := a.writeIntPtr(enc, "display_id", a.DisplayID); err != nil {
			return err
		}
	}
	if a.HasTargetPIDs {
		if err := a.writeIntArray(enc, "target_pids", a.TargetPIDs); err != nil {
			return err
		}
	}
	if a.HasKilledPIDs {
		if err := a.writeIntArray(enc, "killed_pids", a.KilledPIDs); err != nil {
			return err
		}
	}
	if a.HasRemainingPIDs {
		if err := a.writeIntArray(enc, "remaining_pids", a.RemainingPIDs); err != nil {
			return err
		}
	}
	if a.HasRestarted {
		return a.writeNullableBool(enc, "restarted", a.Restarted)
	}
	return nil
}

func (a *AppView) writeIntArray(enc *jsontext.Encoder, name string, values []int) error {
	if err := writeName(enc, name); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.BeginArray); err != nil {
		return err
	}
	for _, value := range values {
		if err := writeInt(enc, value); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndArray)
}

// ShellView is the managed shell execution report. exit_code, output and
// truncated are always present; null means the fact was not obtained.
type ShellView struct {
	Termination string
	ExitCode    *int
	Output      *string
	Truncated   *bool
	OutputError *Error
}

// MarshalJSONTo writes the shell result.
func (s *ShellView) MarshalJSONTo(enc *jsontext.Encoder) error {
	if s == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "termination"); err != nil {
		return err
	}
	if err := writeString(enc, s.Termination); err != nil {
		return err
	}
	if err := writeName(enc, "exit_code"); err != nil {
		return err
	}
	if s.ExitCode == nil {
		if err := writeNull(enc); err != nil {
			return err
		}
	} else if err := writeInt(enc, *s.ExitCode); err != nil {
		return err
	}
	if err := writeName(enc, "output"); err != nil {
		return err
	}
	if s.Output == nil {
		if err := writeNull(enc); err != nil {
			return err
		}
	} else if err := writeString(enc, *s.Output); err != nil {
		return err
	}
	if err := writeName(enc, "truncated"); err != nil {
		return err
	}
	if s.Truncated == nil {
		if err := writeNull(enc); err != nil {
			return err
		}
	} else if err := writeBool(enc, *s.Truncated); err != nil {
		return err
	}
	if s.OutputError != nil {
		if err := writeName(enc, "output_error"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, s.OutputError); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// NotificationView is the notify delivery fact. Saving the message and making
// it visible through the control App are separate facts.
type NotificationView struct {
	State string
	Error *Error
}

// MarshalJSONTo writes the notification branch.
func (n *NotificationView) MarshalJSONTo(enc *jsontext.Encoder) error {
	if n == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "state"); err != nil {
		return err
	}
	if err := writeString(enc, n.State); err != nil {
		return err
	}
	if n.Error != nil {
		if err := writeName(enc, "error"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, n.Error); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// InspectionView is the historical reading material returned by inspect. It
// never grants input authority and never triggers a new capture. Historical
// inspection is always the detailed view: every retained field is expanded and
// no geometry is hidden.
type InspectionView struct {
	ObservationID string
	Origin        string
	Revision      int
	CapturedAt    string
	Scope         string
	// View is declared once on the inspection envelope for the subtree and query
	// scopes. The tree scope declares it in its nested complete tree instead, so
	// the same metadata is never stated twice.
	View      string
	Tree      *ReadTree
	Root      *ReadNode
	Matches   []InspectionMatch
	Total     int
	HasTotal  bool
	Truncated *bool
}

// InspectionMatch is one complete matched subtree and its ancestor ref path.
type InspectionMatch struct {
	Node *ReadNode
	Path []string
}

// MarshalJSONTo writes one inspection branch.
func (v *InspectionView) MarshalJSONTo(enc *jsontext.Encoder) error {
	if v == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	fields := []struct {
		name  string
		value string
	}{
		{"observation_id", v.ObservationID},
		{"origin", v.Origin},
	}
	for _, field := range fields {
		if err := writeName(enc, field.name); err != nil {
			return err
		}
		if err := writeString(enc, field.value); err != nil {
			return err
		}
	}
	if err := writeName(enc, "revision"); err != nil {
		return err
	}
	if err := writeInt(enc, v.Revision); err != nil {
		return err
	}
	if err := writeName(enc, "captured_at"); err != nil {
		return err
	}
	if err := writeString(enc, v.CapturedAt); err != nil {
		return err
	}
	if err := writeName(enc, "scope"); err != nil {
		return err
	}
	if err := writeString(enc, v.Scope); err != nil {
		return err
	}
	if v.Scope != "tree" {
		if err := writeName(enc, "view"); err != nil {
			return err
		}
		if err := writeString(enc, v.View); err != nil {
			return err
		}
	}
	switch v.Scope {
	case "tree":
		if v.Tree != nil {
			if err := writeName(enc, "tree"); err != nil {
				return err
			}
			if err := MarshalJSONTo(enc, v.Tree); err != nil {
				return err
			}
		}
	case "subtree":
		if v.Root != nil {
			if err := writeName(enc, "root"); err != nil {
				return err
			}
			if err := MarshalJSONTo(enc, v.Root); err != nil {
				return err
			}
		}
	case "query":
		if err := writeName(enc, "matches"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for _, match := range v.Matches {
			if err := enc.WriteToken(jsontext.BeginObject); err != nil {
				return err
			}
			if err := writeName(enc, "node"); err != nil {
				return err
			}
			if err := MarshalJSONTo(enc, match.Node); err != nil {
				return err
			}
			if err := writeName(enc, "path"); err != nil {
				return err
			}
			if err := enc.WriteToken(jsontext.BeginArray); err != nil {
				return err
			}
			for _, ref := range match.Path {
				if err := writeString(enc, ref); err != nil {
					return err
				}
			}
			if err := enc.WriteToken(jsontext.EndArray); err != nil {
				return err
			}
			if err := enc.WriteToken(jsontext.EndObject); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return err
		}
		if v.HasTotal {
			if err := writeName(enc, "total"); err != nil {
				return err
			}
			if err := writeInt(enc, v.Total); err != nil {
				return err
			}
		}
		if v.Truncated != nil {
			if err := writeName(enc, "truncated"); err != nil {
				return err
			}
			if err := writeBool(enc, *v.Truncated); err != nil {
				return err
			}
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// DiagnosisView is the bounded diagnose report. It contains only controlled
// phases, codes, times and coverage, never raw logs or user content.
type DiagnosisView struct {
	Summary   string
	Issues    []DiagnosisIssue
	TimingMS  map[string]int64
	Timeline  []DiagnosisEvent
	Coverage  string
	Truncated bool
	Operation string
	Outcome   string
}

// DiagnosisIssue is one controlled phase/code pair.
type DiagnosisIssue struct {
	Phase string
	Code  string
}

// DiagnosisEvent is one short ordered diagnostic event.
type DiagnosisEvent struct {
	At    string
	Phase string
	Code  string
}

// MarshalJSONTo writes the diagnosis object.
func (d *DiagnosisView) MarshalJSONTo(enc *jsontext.Encoder) error {
	if d == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "summary"); err != nil {
		return err
	}
	if err := writeString(enc, d.Summary); err != nil {
		return err
	}
	if err := writeName(enc, "issues"); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.BeginArray); err != nil {
		return err
	}
	for _, issue := range d.Issues {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "phase"); err != nil {
			return err
		}
		if err := writeString(enc, issue.Phase); err != nil {
			return err
		}
		if err := writeName(enc, "code"); err != nil {
			return err
		}
		if err := writeString(enc, issue.Code); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.EndObject); err != nil {
			return err
		}
	}
	if err := enc.WriteToken(jsontext.EndArray); err != nil {
		return err
	}
	if len(d.TimingMS) > 0 {
		if err := writeName(enc, "timing_ms"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		keys := make([]string, 0, len(d.TimingMS))
		for key := range d.TimingMS {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := writeName(enc, key); err != nil {
				return err
			}
			if err := writeInt64(enc, d.TimingMS[key]); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndObject); err != nil {
			return err
		}
	}
	if len(d.Timeline) > 0 {
		if err := writeName(enc, "timeline"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for _, event := range d.Timeline {
			if err := enc.WriteToken(jsontext.BeginObject); err != nil {
				return err
			}
			if err := writeName(enc, "at"); err != nil {
				return err
			}
			if err := writeString(enc, event.At); err != nil {
				return err
			}
			if err := writeName(enc, "phase"); err != nil {
				return err
			}
			if err := writeString(enc, event.Phase); err != nil {
				return err
			}
			if event.Code != "" {
				if err := writeName(enc, "code"); err != nil {
					return err
				}
				if err := writeString(enc, event.Code); err != nil {
					return err
				}
			}
			if err := enc.WriteToken(jsontext.EndObject); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return err
		}
	}
	if err := writeName(enc, "coverage"); err != nil {
		return err
	}
	if err := writeString(enc, d.Coverage); err != nil {
		return err
	}
	if err := writeName(enc, "truncated"); err != nil {
		return err
	}
	if err := writeBool(enc, d.Truncated); err != nil {
		return err
	}
	if d.Operation != "" {
		if err := writeName(enc, "operation_id"); err != nil {
			return err
		}
		if err := writeString(enc, d.Operation); err != nil {
			return err
		}
	}
	if d.Outcome != "" {
		if err := writeName(enc, "outcome"); err != nil {
			return err
		}
		if err := writeString(enc, d.Outcome); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

func writeInt64(enc *jsontext.Encoder, value int64) error {
	return enc.WriteToken(jsontext.Int(value))
}

// Fold applies a conservative Unicode casefold suitable for literal substring
// matching of user-facing labels and package names. It lowercases runes and
// expands the common multi-rune folds (sharp s and Latin ligatures) that a
// plain ToLower would otherwise miss.
func Fold(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range value {
		if expansion, ok := foldExpansions[r]; ok {
			builder.WriteString(expansion)
			continue
		}
		builder.WriteRune(unicode.ToLower(r))
	}
	return builder.String()
}

var foldExpansions = map[rune]string{
	'ß': "ss",  // LATIN SMALL LETTER SHARP S
	'ẞ': "ss",  // LATIN CAPITAL LETTER SHARP S
	'ﬀ': "ff",  // LATIN SMALL LIGATURE FF
	'ﬁ': "fi",  // LATIN SMALL LIGATURE FI
	'ﬂ': "fl",  // LATIN SMALL LIGATURE FL
	'ﬃ': "ffi", // LATIN SMALL LIGATURE FFI
	'ﬄ': "ffl", // LATIN SMALL LIGATURE FFL
	'ﬅ': "st",  // LATIN SMALL LIGATURE LONG S T
	'ﬆ': "st",  // LATIN SMALL LIGATURE ST
}
