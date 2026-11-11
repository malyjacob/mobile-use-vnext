package main

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
)

// The probe restores the public AppView from one readable text projection. The
// projection keeps the two declared nullable app scalars (state_preserved and
// restarted) and the nested nullable close recents_cleaned distinguishable from
// an omitted member, so every Has* flag below is part of the decoded fact.

type appItemView struct {
	Name    string `json:"name"`
	Package string `json:"package"`
}

type appTaskItemView struct {
	TaskID           int    `json:"task_id"`
	Package          string `json:"package"`
	Ownership        string `json:"ownership"`
	OriginDisplay    *int   `json:"origin_display"`
	HasOriginDisplay bool   `json:"-"`
}

type appCloseTaskView struct {
	TaskID            int        `json:"task_id"`
	Outcome           string     `json:"outcome"`
	RecentsCleaned    *bool      `json:"recents_cleaned"`
	HasRecentsCleaned bool       `json:"-"`
	Error             *errorView `json:"error"`
}

type foregroundView struct {
	State   string `json:"state"`
	Package string `json:"package"`
	TaskID  *int   `json:"task_id"`
	HasTask bool   `json:"-"`
	Reason  string `json:"reason"`
}

type appView struct {
	Kind string

	// find / list
	Items        []appItemView
	HasItems     bool
	TaskItems    []appTaskItemView
	HasTaskItems bool
	Total        int
	HasTotal     bool
	Truncated    *bool
	CapturedAt   string

	// list
	DisplayID  *int
	Foreground *foregroundView

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
	Tasks    []appCloseTaskView
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

// UnmarshalJSONFrom records member presence while decoding the typed app
// object. A plain struct with pointers cannot tell null from an omitted
// member, which the public contract requires for state_preserved, restarted and
// recents_cleaned.
func (a *appView) UnmarshalJSONFrom(decoder *jsontext.Decoder) error {
	raw, err := decoder.ReadValue()
	if err != nil {
		return err
	}
	return a.load(raw)
}

var errAppViewShape = errors.New("app view shape invalid")

func (a *appView) load(raw []byte) error {
	members, err := rawObjectMembers(raw)
	if err != nil {
		return err
	}
	kind, found, err := rawStringField(members, "kind")
	if err != nil {
		return err
	}
	if !found || kind == "" {
		return fmt.Errorf("%w: kind missing", errAppViewShape)
	}
	switch kind {
	case "find", "list", "open", "claim", "handoff", "close", "kill":
	default:
		return fmt.Errorf("%w: unknown kind", errAppViewShape)
	}
	a.Kind = kind

	switch kind {
	case "find":
		if a.Items, a.HasItems, err = rawItemsField(members, "items"); err != nil {
			return err
		}
		if a.Total, a.HasTotal, err = rawIntField(members, "total"); err != nil {
			return err
		}
	case "list":
		if a.DisplayID, _, err = rawIntPtrField(members, "display_id"); err != nil {
			return err
		}
		if a.TaskItems, a.HasTaskItems, err = rawTaskItemsField(members, "items"); err != nil {
			return err
		}
		if a.Total, a.HasTotal, err = rawIntField(members, "total"); err != nil {
			return err
		}
		if a.Foreground, err = rawForegroundField(members, "foreground"); err != nil {
			return err
		}
	case "open", "claim", "handoff":
		if a.Package, _, err = rawStringField(members, "package"); err != nil {
			return err
		}
		if a.TaskID, _, err = rawIntPtrField(members, "task_id"); err != nil {
			return err
		}
		if a.SourceDisplayID, _, err = rawIntPtrField(members, "source_display_id"); err != nil {
			return err
		}
		if a.DisplayID, _, err = rawIntPtrField(members, "display_id"); err != nil {
			return err
		}
		if a.Ownership, _, err = rawStringField(members, "ownership"); err != nil {
			return err
		}
		if a.Reused, _, err = rawBoolPtrField(members, "reused"); err != nil {
			return err
		}
		if a.StatePreserved, a.HasStatePreserved, err = rawNullableBoolField(members, "state_preserved"); err != nil {
			return err
		}
		if a.ForegroundRestore, _, err = rawStringField(members, "foreground_restore"); err != nil {
			return err
		}
	case "close":
		if a.DisplayID, _, err = rawIntPtrField(members, "display_id"); err != nil {
			return err
		}
		if a.Tasks, a.HasTasks, err = rawCloseTasksField(members, "tasks"); err != nil {
			return err
		}
		if a.Complete, _, err = rawBoolPtrField(members, "complete"); err != nil {
			return err
		}
	case "kill":
		if a.Package, _, err = rawStringField(members, "package"); err != nil {
			return err
		}
		if a.SourceTaskID, _, err = rawIntPtrField(members, "source_task_id"); err != nil {
			return err
		}
		if a.DisplayID, _, err = rawIntPtrField(members, "display_id"); err != nil {
			return err
		}
		if a.TargetPIDs, a.HasTargetPIDs, err = rawIntSliceField(members, "target_pids"); err != nil {
			return err
		}
		if a.KilledPIDs, a.HasKilledPIDs, err = rawIntSliceField(members, "killed_pids"); err != nil {
			return err
		}
		if a.RemainingPIDs, a.HasRemainingPIDs, err = rawIntSliceField(members, "remaining_pids"); err != nil {
			return err
		}
		if a.Restarted, a.HasRestarted, err = rawNullableBoolField(members, "restarted"); err != nil {
			return err
		}
	}

	// The shared find/list tail is present for both kinds.
	if kind == "find" || kind == "list" {
		if a.Truncated, _, err = rawBoolPtrField(members, "truncated"); err != nil {
			return err
		}
		if a.CapturedAt, _, err = rawStringField(members, "captured_at"); err != nil {
			return err
		}
	}
	return nil
}

// rawObjectMembers reads one object into raw member values and rejects
// duplicate names and trailing content.
func rawObjectMembers(raw []byte) (map[string]jsontext.Value, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	if decoder.PeekKind() != '{' {
		return nil, errAppViewShape
	}
	if _, err := decoder.ReadToken(); err != nil {
		return nil, err
	}
	members := make(map[string]jsontext.Value)
	for decoder.PeekKind() != '}' {
		name, err := decoder.ReadToken()
		if err != nil {
			return nil, err
		}
		if name.Kind() != '"' {
			return nil, errAppViewShape
		}
		key := name.String()
		if _, duplicate := members[key]; duplicate {
			return nil, errJSONDuplicate
		}
		value, err := decoder.ReadValue()
		if err != nil {
			return nil, err
		}
		members[key] = value.Clone()
	}
	if _, err := decoder.ReadToken(); err != nil {
		return nil, err
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errJSONTrailing
	}
	return members, nil
}

func rawStringField(members map[string]jsontext.Value, name string) (string, bool, error) {
	raw, found := members[name]
	if !found {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, err
	}
	return value, true, nil
}

func rawIntField(members map[string]jsontext.Value, name string) (int, bool, error) {
	raw, found := members[name]
	if !found {
		return 0, false, nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, true, err
	}
	return value, true, nil
}

// rawIntPtrField reports a present member with a null value as an error: these
// paths are not declared nullable.
func rawIntPtrField(members map[string]jsontext.Value, name string) (*int, bool, error) {
	raw, found := members[name]
	if !found {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, errAppViewShape
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, true, err
	}
	return &value, true, nil
}

func rawBoolPtrField(members map[string]jsontext.Value, name string) (*bool, bool, error) {
	raw, found := members[name]
	if !found {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, errAppViewShape
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, true, err
	}
	return &value, true, nil
}

// rawNullableBoolField keeps the difference between an omitted member and an
// explicit null: present=true with value=nil means null.
func rawNullableBoolField(members map[string]jsontext.Value, name string) (*bool, bool, error) {
	raw, found := members[name]
	if !found {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, true, err
	}
	return &value, true, nil
}

func rawIntSliceField(members map[string]jsontext.Value, name string) ([]int, bool, error) {
	raw, found := members[name]
	if !found {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, errAppViewShape
	}
	var values []int
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, true, err
	}
	if values == nil {
		values = []int{}
	}
	return values, true, nil
}

func rawItemsField(members map[string]jsontext.Value, name string) ([]appItemView, bool, error) {
	raw, found := members[name]
	if !found {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, errAppViewShape
	}
	var items []appItemView
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, true, err
	}
	if items == nil {
		items = []appItemView{}
	}
	return items, true, nil
}

func rawTaskItemsField(members map[string]jsontext.Value, name string) ([]appTaskItemView, bool, error) {
	raw, found := members[name]
	if !found {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, errAppViewShape
	}
	rawItems, err := rawArray(raw)
	if err != nil {
		return nil, true, err
	}
	items := make([]appTaskItemView, 0, len(rawItems))
	for _, rawItem := range rawItems {
		itemMembers, err := rawObjectMembers(rawItem)
		if err != nil {
			return nil, true, err
		}
		item := appTaskItemView{}
		if item.TaskID, _, err = rawIntField(itemMembers, "task_id"); err != nil {
			return nil, true, err
		}
		if item.Package, _, err = rawStringField(itemMembers, "package"); err != nil {
			return nil, true, err
		}
		if item.Ownership, _, err = rawStringField(itemMembers, "ownership"); err != nil {
			return nil, true, err
		}
		if item.OriginDisplay, item.HasOriginDisplay, err = rawIntPtrField(itemMembers, "origin_display"); err != nil {
			return nil, true, err
		}
		items = append(items, item)
	}
	return items, true, nil
}

func rawCloseTasksField(members map[string]jsontext.Value, name string) ([]appCloseTaskView, bool, error) {
	raw, found := members[name]
	if !found {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, errAppViewShape
	}
	rawTasks, err := rawArray(raw)
	if err != nil {
		return nil, true, err
	}
	tasks := make([]appCloseTaskView, 0, len(rawTasks))
	for _, rawTask := range rawTasks {
		taskMembers, err := rawObjectMembers(rawTask)
		if err != nil {
			return nil, true, err
		}
		task := appCloseTaskView{}
		if task.TaskID, _, err = rawIntField(taskMembers, "task_id"); err != nil {
			return nil, true, err
		}
		if task.Outcome, _, err = rawStringField(taskMembers, "outcome"); err != nil {
			return nil, true, err
		}
		if task.RecentsCleaned, task.HasRecentsCleaned, err = rawNullableBoolField(taskMembers, "recents_cleaned"); err != nil {
			return nil, true, err
		}
		if rawError, exists := taskMembers["error"]; exists {
			if bytes.Equal(bytes.TrimSpace(rawError), []byte("null")) {
				return nil, true, errAppViewShape
			}
			var nested errorView
			if err := json.Unmarshal(rawError, &nested); err != nil {
				return nil, true, err
			}
			task.Error = &nested
		}
		tasks = append(tasks, task)
	}
	return tasks, true, nil
}

func rawForegroundField(members map[string]jsontext.Value, name string) (*foregroundView, error) {
	raw, found := members[name]
	if !found {
		return nil, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errAppViewShape
	}
	foregroundMembers, err := rawObjectMembers(raw)
	if err != nil {
		return nil, err
	}
	foreground := &foregroundView{}
	if foreground.State, _, err = rawStringField(foregroundMembers, "state"); err != nil {
		return nil, err
	}
	if foreground.Package, _, err = rawStringField(foregroundMembers, "package"); err != nil {
		return nil, err
	}
	if foreground.Reason, _, err = rawStringField(foregroundMembers, "reason"); err != nil {
		return nil, err
	}
	if foreground.TaskID, foreground.HasTask, err = rawNullableIntPtrField(foregroundMembers, "task_id"); err != nil {
		return nil, err
	}
	return foreground, nil
}

func rawNullableIntPtrField(members map[string]jsontext.Value, name string) (*int, bool, error) {
	raw, found := members[name]
	if !found {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, true, err
	}
	return &value, true, nil
}

func rawArray(raw jsontext.Value) ([]jsontext.Value, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	if decoder.PeekKind() != '[' {
		return nil, errAppViewShape
	}
	if _, err := decoder.ReadToken(); err != nil {
		return nil, err
	}
	values := make([]jsontext.Value, 0)
	for decoder.PeekKind() != ']' {
		value, err := decoder.ReadValue()
		if err != nil {
			return nil, err
		}
		values = append(values, value.Clone())
	}
	if _, err := decoder.ReadToken(); err != nil {
		return nil, err
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errJSONTrailing
	}
	return values, nil
}
