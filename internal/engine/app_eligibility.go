package engine

import (
	"slices"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

const (
	closeEligibilityTTL   = 5 * time.Minute
	maxCloseEligibilities = 64
)

type closeEligibility struct {
	Package           string
	AndroidUserID     int
	AppID             int
	AppUID            int
	FirstInstallTime  string
	SignatureHash     string
	DisplayID         int
	DisplayGeneration string
	SessionEpoch      int64
	ClosedAt          time.Time
	ExecutionIdentity device.BootIdentity
}

func addCloseEligibilityLocked(session *sessionState, record closeEligibility) {
	records := session.CloseEligibilities
	oldest := 0
	for i, item := range records {
		if item.Package == record.Package && item.AndroidUserID == record.AndroidUserID {
			records[i] = record
			return
		}
		if item.ClosedAt.Before(records[oldest].ClosedAt) {
			oldest = i
		}
	}
	if len(records) >= maxCloseEligibilities {
		records = slices.Delete(records, oldest, oldest+1)
	}
	session.CloseEligibilities = append(records, record)
}

// closeEligibilityLocked returns a record only when exactly one eligible user
// remains for the package. The caller must still compare fresh device identity.
func closeEligibilityLocked(session *sessionState, pkg string, now time.Time) *closeEligibility {
	pruneCloseEligibilitiesLocked(session, now)
	var found *closeEligibility
	for i := range session.CloseEligibilities {
		record := &session.CloseEligibilities[i]
		if record.Package == pkg {
			if found != nil {
				return nil
			}
			found = record
		}
	}
	return found
}

func pruneCloseEligibilitiesLocked(session *sessionState, now time.Time) {
	session.CloseEligibilities = slices.DeleteFunc(session.CloseEligibilities, func(record closeEligibility) bool {
		return record.SessionEpoch != session.Epoch || record.DisplayID != session.Display.ID ||
			record.DisplayGeneration != session.Display.Generation ||
			now.Sub(record.ClosedAt) >= closeEligibilityTTL
	})
}

func revokeCloseEligibilityLocked(session *sessionState, pkg string) {
	session.CloseEligibilities = slices.DeleteFunc(session.CloseEligibilities, func(record closeEligibility) bool {
		return record.Package == pkg
	})
}

func consumeCloseEligibilityLocked(session *sessionState, pkg string, user int) {
	session.CloseEligibilities = slices.DeleteFunc(session.CloseEligibilities, func(record closeEligibility) bool {
		return record.Package == pkg && record.AndroidUserID == user
	})
}

func (record closeEligibility) matches(identity device.PackageIdentity, execution device.BootIdentity) bool {
	return record.ExecutionIdentity == execution && record.Package == identity.Package &&
		record.AndroidUserID == identity.AndroidUserID && record.AppID == identity.AppID &&
		record.AppUID == identity.AppUID && record.FirstInstallTime == identity.FirstInstallTime &&
		record.SignatureHash == identity.SignatureHash
}
