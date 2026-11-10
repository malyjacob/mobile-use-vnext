package device

// RecentTasksResult contains controlled index facts, never raw dumpsys text.
type RecentTasksResult struct {
	IDs   map[int]bool
	State string // transport_unavailable/nonzero_exit/format_unrecognized/known_empty/known_present
}
