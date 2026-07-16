package license

import (
	"fmt"
	"sort"
	"time"
)

// utc is the fixed evaluation zone. All license math runs in UTC so results are
// independent of device time zone, matching the Apple gregorian/UTC calendar.
var utc = time.UTC

// UTCDate builds a midnight-UTC date, mirroring mobileLicenseUTCDate.
func UTCDate(year, month, day int) time.Time {
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, utc)
}

// daysIn returns the number of days in the given year/month.
func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, utc).Day()
}

// addMonthsClamped adds months, clamping the day to the last valid day of the
// target month (e.g. Jan 31 + 1 month = Feb 28). This matches Calendar's
// date(byAdding:) behavior, unlike time.AddDate which overflows.
func addMonthsClamped(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	hh, mm, ss := t.Clock()
	total := int(m) - 1 + months
	ny := y + total/12
	nm := total % 12
	if nm < 0 {
		nm += 12
		ny--
	}
	month := time.Month(nm + 1)
	if last := daysIn(ny, month); d > last {
		d = last
	}
	return time.Date(ny, month, d, hh, mm, ss, t.Nanosecond(), t.Location())
}

// addYearsClamped adds whole years with day clamping (Feb 29 -> Feb 28).
func addYearsClamped(t time.Time, years int) time.Time {
	return addMonthsClamped(t, years*12)
}

// TrialEndDate returns the end of the trial that started at start.
func TrialEndDate(start time.Time) time.Time {
	return addMonthsClamped(start, TrialMonths)
}

// startOfDayUTC truncates to midnight UTC.
func startOfDayUTC(t time.Time) time.Time {
	u := t.In(utc)
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, utc)
}

// EnsureTrialStart returns a snapshot guaranteed to have a trial start date,
// seeding it to now when absent. Callers persist the returned snapshot so the
// trial origin survives restarts and reinstalls.
func EnsureTrialStart(s Snapshot, now time.Time) Snapshot {
	out := s
	if out.TrialStartDate == nil {
		n := now
		out.TrialStartDate = &n
	}
	out.CachedAt = now
	return out
}

// UpdateCutoffDate computes the update entitlement cutoff: one year from the
// lifetime purchase, extended one year per active paid update, each extending
// from the later of the current cutoff or the update's purchase date.
func UpdateCutoffDate(lifetimePurchase time.Time, transactions []Transaction) time.Time {
	cutoff := addYearsClamped(lifetimePurchase, 1)

	paid := make([]Transaction, 0, len(transactions))
	for _, t := range transactions {
		if t.Kind() == ProductPaidUpdate && t.IsActive() {
			paid = append(paid, t)
		}
	}
	sort.Slice(paid, func(i, j int) bool {
		return paid[i].PurchaseDate.Before(paid[j].PurchaseDate)
	})
	for _, u := range paid {
		start := cutoff
		if u.PurchaseDate.After(start) {
			start = u.PurchaseDate
		}
		cutoff = addYearsClamped(start, 1)
	}
	return cutoff
}

// offlineGraceEnd returns the end of the offline grace window, if one applies.
func offlineGraceEnd(s Snapshot) *time.Time {
	if s.LastVerificationFailedAt == nil {
		return nil
	}
	if s.LastVerifiedAt != nil && s.LastVerificationFailedAt.Before(*s.LastVerifiedAt) {
		return nil
	}
	end := s.LastVerificationFailedAt.AddDate(0, 0, OfflineGraceDays)
	return &end
}

// Evaluate resolves the access decision for a snapshot at now.
func Evaluate(s Snapshot, features []Feature, now time.Time) Decision {
	if features == nil {
		features = DefaultFeatures()
	}

	var trialEndsAt *time.Time
	trialActive := false
	trialDaysRemaining := 0
	if s.TrialStartDate != nil {
		end := TrialEndDate(*s.TrialStartDate)
		trialEndsAt = &end
		trialActive = now.Before(end)
		days := int(startOfDayUTC(end).Sub(startOfDayUTC(now)).Hours() / 24)
		if days < 0 {
			days = 0
		}
		trialDaysRemaining = days
	}

	active := make([]Transaction, 0, len(s.Transactions))
	for _, t := range s.Transactions {
		if t.IsActive() {
			active = append(active, t)
		}
	}

	var lifetime *Transaction
	for i := range active {
		if active[i].Kind() != ProductLifetimeUnlock {
			continue
		}
		if lifetime == nil || active[i].PurchaseDate.Before(lifetime.PurchaseDate) {
			t := active[i]
			lifetime = &t
		}
	}

	var cutoff *time.Time
	if lifetime != nil {
		c := UpdateCutoffDate(lifetime.PurchaseDate, active)
		cutoff = &c
	}

	var graceEnd *time.Time
	if end := offlineGraceEnd(s); end != nil && now.Before(*end) {
		graceEnd = end
	}

	var reason AccessReason
	switch {
	case trialActive:
		reason = ReasonTrial
	case lifetime != nil && graceEnd != nil:
		reason = ReasonOfflineGrace
	case lifetime != nil:
		reason = ReasonLifetime
	default:
		reason = ReasonLocked
	}

	unlocked := []FeatureID{}
	switch reason {
	case ReasonTrial:
		for _, f := range features {
			unlocked = append(unlocked, f.ID)
		}
	case ReasonLifetime, ReasonOfflineGrace:
		if cutoff != nil {
			for _, f := range features {
				if !f.ReleaseDate.After(*cutoff) {
					unlocked = append(unlocked, f.ID)
				}
			}
		}
	case ReasonLocked:
		// nothing unlocked
	}

	d := Decision{
		Reason:             reason,
		TrialStartDate:     s.TrialStartDate,
		TrialEndsAt:        trialEndsAt,
		TrialDaysRemaining: trialDaysRemaining,
		HasLifetimeUnlock:  lifetime != nil,
		UpdateCutoffDate:   cutoff,
		UnlockedFeatureIDs: unlocked,
	}
	if reason == ReasonOfflineGrace {
		d.OfflineGraceEndsAt = graceEnd
	}
	return d
}

// CanInstallUpdate reports whether a release may be installed under the current
// decision. Trial installs anything; locked installs nothing; a licensed device
// installs releases dated on or before its cutoff. When the release date is
// unknown, it fails closed against the cutoff using now.
func CanInstallUpdate(d Decision, publishedAt *time.Time, now time.Time) bool {
	switch d.Reason {
	case ReasonTrial:
		return true
	case ReasonLocked:
		return false
	case ReasonLifetime, ReasonOfflineGrace:
		if d.UpdateCutoffDate == nil {
			return false
		}
		if publishedAt == nil {
			return !now.After(*d.UpdateCutoffDate)
		}
		return !publishedAt.After(*d.UpdateCutoffDate)
	default:
		return false
	}
}

// PaidUpdatePolicyCopy is the strict cutoff-policy diagnostic text.
func PaidUpdatePolicyCopy(cutoff time.Time) string {
	return fmt.Sprintf(
		"The ClambHook license includes all updates released through %s. Versions released during that window remain usable. Updates released after that date, including critical, bug, and security updates, require a USD 9.99 update-year renewal.",
		formatDate(cutoff),
	)
}

func formatDate(t time.Time) string {
	return t.In(utc).Format("Jan 2, 2006")
}
