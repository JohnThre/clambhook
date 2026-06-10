package license

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func evaluateGrant(dev DeviceRecord, transactions []LicenseTransaction, now time.Time, cfg Config) GrantSnapshot {
	active := activeTransactions(transactions)
	lifetime := firstLifetime(active)
	var cutoff *time.Time
	if lifetime != nil {
		value := updateCutoffDate(lifetime.PurchaseDate, active)
		cutoff = &value
	}

	trialStart := dev.TrialStartDate
	trialEnd := dev.TrialEndsAt
	reason := AccessReasonLocked
	if now.Before(trialEnd) {
		reason = AccessReasonTrial
	} else if lifetime != nil {
		reason = AccessReasonLifetime
	}

	return GrantSnapshot{
		Reason:            reason,
		TrialStartDate:    &trialStart,
		TrialEndsAt:       &trialEnd,
		HasLifetimeUnlock: lifetime != nil,
		UpdateCutoffDate:  cutoff,
		Transactions:      orderedTransactions(active),
	}
}

func activeTransactions(transactions []LicenseTransaction) []LicenseTransaction {
	active := make([]LicenseTransaction, 0, len(transactions))
	for _, tx := range transactions {
		if tx.RevocationDate == nil {
			active = append(active, tx)
		}
	}
	return active
}

func firstLifetime(transactions []LicenseTransaction) *LicenseTransaction {
	var lifetime *LicenseTransaction
	for i := range transactions {
		if transactions[i].ProductID != LifetimeUnlockProductID {
			continue
		}
		if lifetime == nil || transactions[i].PurchaseDate.Before(lifetime.PurchaseDate) {
			lifetime = &transactions[i]
		}
	}
	return lifetime
}

func updateCutoffDate(lifetimePurchaseDate time.Time, transactions []LicenseTransaction) time.Time {
	cutoff := lifetimePurchaseDate.AddDate(1, 0, 0)
	updates := make([]LicenseTransaction, 0)
	for _, tx := range transactions {
		if isFeatureUpdateProduct(tx.ProductID) {
			updates = append(updates, tx)
		}
	}
	sort.Slice(updates, func(i, j int) bool {
		return updates[i].PurchaseDate.Before(updates[j].PurchaseDate)
	})
	for _, tx := range updates {
		start := cutoff
		if tx.PurchaseDate.After(start) {
			start = tx.PurchaseDate
		}
		cutoff = start.AddDate(1, 0, 0)
	}
	return cutoff
}

func orderedTransactions(transactions []LicenseTransaction) []LicenseTransaction {
	out := append([]LicenseTransaction(nil), transactions...)
	sort.Slice(out, func(i, j int) bool {
		if productSortKey(out[i].ProductID) == productSortKey(out[j].ProductID) {
			return out[i].PurchaseDate.Before(out[j].PurchaseDate)
		}
		return productSortKey(out[i].ProductID) < productSortKey(out[j].ProductID)
	})
	return out
}

func productSortKey(productID string) string {
	if productID == LifetimeUnlockProductID {
		return "0000"
	}
	if strings.HasPrefix(productID, FeatureUpdatePrefix) {
		return "1" + strings.TrimPrefix(productID, FeatureUpdatePrefix)
	}
	return "9" + productID
}

func isKnownProduct(productID string) bool {
	return productID == LifetimeUnlockProductID || isFeatureUpdateProduct(productID)
}

func isFeatureUpdateProduct(productID string) bool {
	suffix := strings.TrimPrefix(productID, FeatureUpdatePrefix)
	if suffix == productID || len(suffix) != 4 {
		return false
	}
	_, err := strconv.Atoi(suffix)
	return err == nil
}

func trialEndDate(start time.Time) time.Time {
	return addMonthsClampedUTC(start, TrialMonths)
}

func addMonthsClampedUTC(start time.Time, months int) time.Time {
	start = start.UTC()
	year, month, day := start.Date()
	hour, minute, second := start.Clock()
	targetFirst := time.Date(year, month+time.Month(months), 1, hour, minute, second, start.Nanosecond(), time.UTC)
	targetLastDay := targetFirst.AddDate(0, 1, -1).Day()
	if day > targetLastDay {
		day = targetLastDay
	}
	return time.Date(targetFirst.Year(), targetFirst.Month(), day, hour, minute, second, start.Nanosecond(), time.UTC)
}

func buildSignedGrant(dev DeviceRecord, snapshot GrantSnapshot, now time.Time, cfg Config) (LicenseGrant, error) {
	expires := now.Add(cfg.OfflineGrace)
	grant := LicenseGrant{
		Version:            1,
		IssuedAt:           now,
		ExpiresAt:          expires,
		InstallHash:        dev.InstallHash,
		KeyHash:            dev.KeyHash,
		Reason:             snapshot.Reason,
		TrialStartDate:     snapshot.TrialStartDate,
		TrialEndsAt:        snapshot.TrialEndsAt,
		HasLifetimeUnlock:  snapshot.HasLifetimeUnlock,
		UpdateCutoffDate:   snapshot.UpdateCutoffDate,
		OfflineGraceEndsAt: snapshot.OfflineGraceEndsAt,
		Transactions:       snapshot.Transactions,
	}
	signature, err := signGrant(cfg.GrantSigningSecret, grant)
	if err != nil {
		return LicenseGrant{}, fmt.Errorf("sign grant: %w", err)
	}
	grant.Signature = signature
	return grant, nil
}
