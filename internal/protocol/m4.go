package protocol

import (
	"encoding/json/jsontext"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// M4 request shapes. Each tool admits exactly the members listed by its
// presence struct; every optional member is omitted from the wire when not
// supplied (never encoded as an explicit null).

// AppsRequest is apps(query=None).
type AppsRequest struct {
	Query    string
	HasQuery bool
}

// OpenAppRequest is open_app(observation_id, app).
type OpenAppRequest struct {
	ObservationID string
	App           string
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

// InspectRequest is inspect(observation_id, node=None, query=None).
type InspectRequest struct {
	ObservationID string
	Node          string
	Query         string
	HasNode       bool
	HasQuery      bool
}

// KeepaliveRequest is keepalive(reference).
type KeepaliveRequest struct {
	Reference string
}

// DiagnoseRequest is diagnose(reference=None).
type DiagnoseRequest struct {
	Reference string
	HasRef    bool
}

// DecodeApps admits apps(query).
func DecodeApps(body []byte) (*AppsRequest, *APIError) {
	var presence struct {
		Query jsontext.Value `json:"query"`
	}
	var wire struct {
		Query string `json:"query"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.Query) > 0 && string(presence.Query) == "null" {
		return nil, Invalid("query 不接受 null；省略该字段表示列出全部")
	}
	if utf8.RuneCountInString(wire.Query) > MaxQueryRunes {
		return nil, Invalid("query 超过长度上限").WithDetail("limit", MaxQueryRunes)
	}
	return &AppsRequest{Query: wire.Query, HasQuery: len(presence.Query) > 0}, nil
}

// DecodeOpenApp admits open_app(observation_id, app).
func DecodeOpenApp(body []byte) (*OpenAppRequest, *APIError) {
	var presence struct {
		ObservationID jsontext.Value `json:"observation_id"`
		App           jsontext.Value `json:"app"`
	}
	var wire struct {
		ObservationID string `json:"observation_id"`
		App           string `json:"app"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.ObservationID) == 0 || string(presence.ObservationID) == "null" {
		return nil, Invalid("open_app 需要 observation_id")
	}
	if len(presence.App) == 0 || string(presence.App) == "null" {
		return nil, Invalid("open_app 需要 app 包名")
	}
	if err := checkReference(wire.ObservationID); err != nil {
		return nil, err
	}
	if !ValidPackageName(wire.App) {
		return nil, Invalid("app 必须是明确的应用包名")
	}
	return &OpenAppRequest{ObservationID: wire.ObservationID, App: wire.App}, nil
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

// DecodeInspect admits inspect(observation_id, node, query). node and query
// are mutually exclusive and both are literals, never selector languages.
func DecodeInspect(body []byte) (*InspectRequest, *APIError) {
	var presence struct {
		ObservationID jsontext.Value `json:"observation_id"`
		Node          jsontext.Value `json:"node"`
		Query         jsontext.Value `json:"query"`
	}
	var wire struct {
		ObservationID string `json:"observation_id"`
		Node          string `json:"node"`
		Query         string `json:"query"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.ObservationID) == 0 || string(presence.ObservationID) == "null" {
		return nil, Invalid("inspect 需要 observation_id")
	}
	if err := checkReference(wire.ObservationID); err != nil {
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
		ObservationID: wire.ObservationID,
		Node:          wire.Node, Query: wire.Query,
		HasNode: hasNode, HasQuery: hasQuery,
	}, nil
}

// DecodeKeepalive admits keepalive(reference).
func DecodeKeepalive(body []byte) (*KeepaliveRequest, *APIError) {
	var presence struct {
		Reference jsontext.Value `json:"reference"`
	}
	var wire struct {
		Reference string `json:"reference"`
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
	return &KeepaliveRequest{Reference: wire.Reference}, nil
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

// AppEntry is one discovered launchable application. Name is the localized
// launcher label and is user-facing content; it must never be logged.
type AppEntry struct {
	Name    string
	Package string
}

// AppsView is the apps tool result. Items always encodes an array, including
// the empty list; total counts all matches before the byte budget applies.
type AppsView struct {
	Items      []AppEntry
	Total      int
	Truncated  bool
	CapturedAt string
}

// MarshalJSONTo writes the apps discovery result.
func (a *AppsView) MarshalJSONTo(enc *jsontext.Encoder) error {
	if a == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "items"); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.BeginArray); err != nil {
		return err
	}
	for _, entry := range a.Items {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "name"); err != nil {
			return err
		}
		if err := writeString(enc, entry.Name); err != nil {
			return err
		}
		if err := writeName(enc, "package"); err != nil {
			return err
		}
		if err := writeString(enc, entry.Package); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.EndObject); err != nil {
			return err
		}
	}
	if err := enc.WriteToken(jsontext.EndArray); err != nil {
		return err
	}
	if err := writeName(enc, "total"); err != nil {
		return err
	}
	if err := writeInt(enc, a.Total); err != nil {
		return err
	}
	if err := writeName(enc, "truncated"); err != nil {
		return err
	}
	if err := writeBool(enc, a.Truncated); err != nil {
		return err
	}
	if err := writeName(enc, "captured_at"); err != nil {
		return err
	}
	if err := writeString(enc, a.CapturedAt); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// AppView is one open_app action fact. It carries only the package and owned
// task identity, never the launch component or intent body.
type AppView struct {
	Package        string
	TaskID         int
	DisplayID      int
	Reused         bool
	StatePreserved *bool
}

// MarshalJSONTo writes the open_app action fact.
func (a *AppView) MarshalJSONTo(enc *jsontext.Encoder) error {
	if a == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "package"); err != nil {
		return err
	}
	if err := writeString(enc, a.Package); err != nil {
		return err
	}
	if err := writeName(enc, "task_id"); err != nil {
		return err
	}
	if err := writeInt(enc, a.TaskID); err != nil {
		return err
	}
	if err := writeName(enc, "display_id"); err != nil {
		return err
	}
	if err := writeInt(enc, a.DisplayID); err != nil {
		return err
	}
	if err := writeName(enc, "reused"); err != nil {
		return err
	}
	if err := writeBool(enc, a.Reused); err != nil {
		return err
	}
	if a.StatePreserved != nil {
		if err := writeName(enc, "state_preserved"); err != nil {
			return err
		}
		if err := writeBool(enc, *a.StatePreserved); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
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
// never grants input authority and never triggers a new capture.
type InspectionView struct {
	ObservationID string
	Origin        string
	Revision      int
	CapturedAt    string
	Scope         string
	Tree          *Tree
	Root          *Node
	Matches       []InspectionMatch
	Total         int
	HasTotal      bool
	Truncated     *bool
}

// InspectionMatch is one complete matched subtree and its ancestor ref path.
type InspectionMatch struct {
	Node *Node
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
