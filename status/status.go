package status

import (
	"fmt"
	"github.com/fluidkeys/crypto/openpgp"
	"github.com/fluidkeys/fluidkeys/pgpkey"
	"sort"
	"time"
)

// GetKeyWarnings returns a slice of KeyWarnings indicating problems found
// with the given PgpKey.
func GetKeyWarnings(key pgpkey.PgpKey) []KeyWarning {
	var warnings []KeyWarning

	warnings = append(warnings, getPrimaryKeyWarnings(key)...)
	warnings = append(warnings, getEncryptionSubkeyWarnings(key)...)
	return warnings
}

func getEncryptionSubkeyWarnings(key pgpkey.PgpKey) []KeyWarning {
	encryptionSubkey := getMostRecentEncryptionSubkey(key)


	if encryptionSubkey == nil {
		return []KeyWarning{KeyWarning{Type: NoValidEncryptionSubkey}}
	}

	subkeyId := encryptionSubkey.PublicKey.KeyId

	now := time.Now()
	var warnings []KeyWarning

	hasExpiry, expiry := getSubkeyExpiry(*encryptionSubkey)

	if hasExpiry {
		nextRotation := calculateNextRotationTime(*expiry)

		if isExpired(*expiry, now) {
			warning := KeyWarning{
				Type: NoValidEncryptionSubkey,
			}
			warnings = append(warnings, warning)

		} else if isOverdueForRotation(nextRotation, now) {
			warning := KeyWarning{
				Type:            SubkeyOverdueForRotation,
				SubkeyId:        subkeyId,
				DaysUntilExpiry: getDaysUntilExpiry(nextRotation, now),
			}
			warnings = append(warnings, warning)

		} else if isDueForRotation(nextRotation, now) {
			warning := KeyWarning{
				Type:     SubkeyDueForRotation,
				SubkeyId: subkeyId,
			}
			warnings = append(warnings, warning)
		}

		if isExpiryTooLong(*expiry, now) {
			warning := KeyWarning{
				Type:     SubkeyLongExpiry,
				SubkeyId: subkeyId,
			}
			warnings = append(warnings, warning)
		}
	} else { // no expiry
		warning := KeyWarning{
			Type:     SubkeyNoExpiry,
			SubkeyId: subkeyId,
		}
		warnings = append(warnings, warning)
	}

	return warnings
}

func getPrimaryKeyWarnings(key pgpkey.PgpKey) []KeyWarning {
	var warnings []KeyWarning

	now := time.Now()
	hasExpiry, expiry := getEarliestUidExpiry(key)

	if hasExpiry {
		nextRotation := calculateNextRotationTime(*expiry)

		if isExpired(*expiry, now) {
			warning := KeyWarning{
				Type:            PrimaryKeyExpired,
				DaysSinceExpiry: getDaysSinceExpiry(*expiry, now),
			}
			warnings = append(warnings, warning)

		} else if isOverdueForRotation(nextRotation, now) {
			warning := KeyWarning{
				Type:            PrimaryKeyOverdueForRotation,
				DaysUntilExpiry: getDaysUntilExpiry(*expiry, now),
			}

			warnings = append(warnings, warning)

		} else if isDueForRotation(nextRotation, now) {
			warning := KeyWarning{Type: PrimaryKeyDueForRotation}
			warnings = append(warnings, warning)
		}

		if isExpiryTooLong(*expiry, now) {
			warning := KeyWarning{Type: PrimaryKeyLongExpiry}
			warnings = append(warnings, warning)
		}
	} else { // no expiry
		warning := KeyWarning{Type: PrimaryKeyNoExpiry}
		warnings = append(warnings, warning)
	}

	return warnings
}

const tenDays time.Duration = time.Duration(time.Hour * 24 * 10)
const thirtyDays time.Duration = time.Duration(time.Hour * 24 * 30)
const fortyFiveDays time.Duration = time.Duration(time.Hour * 24 * 45)

// CalculateNextRotationTime returns 30 days before the earliest expiry time on
// the key.
// If the key doesn't expire, it returns nil.
func calculateNextRotationTime(expiry time.Time) time.Time {
	return expiry.Add(-thirtyDays)
}

// isExpiryTooLong returns true if the expiry is too far in the future.
//
// It's important not to raise this warning for expiries that we've set
// ourselves.
// We use `nextExpiryTime` such that when we set an expiry date it's *exactly*
// on the cusp of being too long, and can only get shorter after that point.
func isExpiryTooLong(expiry time.Time, now time.Time) bool {
	latestAcceptableExpiry := nextExpiryTime(now)
	return expiry.After(latestAcceptableExpiry)
}

func isExpired(expiry time.Time, now time.Time) bool {
	return expiry.Before(now)
}

// isOverdueForRotation returns true if `now` is more than 10 days after
// nextRotation
func isOverdueForRotation(nextRotation time.Time, now time.Time) bool {
	overdueTime := nextRotation.Add(tenDays)
	return overdueTime.Before(now)
}

// isDueForRotation returns true if `now` is any time after the key's next
// rotation time
func isDueForRotation(nextRotation time.Time, now time.Time) bool {
	return nextRotation.Before(now)
}

// getDaysSinceExpiry returns the number of whole 24-hour periods until the
// `expiry`
func getDaysUntilExpiry(expiry time.Time, now time.Time) uint {
	days := inDays(expiry.Sub(now))
	if days < 0 {
		panic(fmt.Errorf("getDaysUntilExpiry: expiry has already passed: %v", expiry))
	}
	return uint(days)
}

func inDays(duration time.Duration) int {
	return int(duration.Seconds() / 86400)
}

// getDaysSinceExpiry returns the number of whole 24-hour periods that have
// elapsed since `expiry`
func getDaysSinceExpiry(expiry time.Time, now time.Time) uint {
	days := inDays(now.Sub(expiry))
	if days < 0 {
		panic(fmt.Errorf("getDaysSinceExpiry: expiry is in the future: %v", expiry))
	}
	return uint(days)
}

func getSubkeyExpiry(subkey openpgp.Subkey) (bool, *time.Time) {
	return calculateExpiry(
		subkey.PublicKey.CreationTime, // not to be confused with the time of the *signature*
		subkey.Sig.KeyLifetimeSecs,
	)
}

// getEarliestUidExpiry is roughly equivalent to "the expiry of the primary key"
//
// returns (hasExpiry, expiryTime) where hasExpiry is a bool indicating if
// an expiry is actually set
//
// Each User ID is signed with an expiry. When the last one is expired, the
// primary key is treated as expired (even though it's just the UIDs).
//
// If there are multiple UIDs we choose the earliest expiry, since that'll
// disrupt the working of the key (plus, Keyflow advises not to use multiple
// UIDs at all, let alone different expiry dates, so this is an edge-case)
func getEarliestUidExpiry(key pgpkey.PgpKey) (bool, *time.Time) {
	var allExpiryTimes []time.Time

	for _, id := range key.Identities {
		hasExpiry, expiryTime := calculateExpiry(
			key.PrimaryKey.CreationTime, // not to be confused with the time of the *signature*
			id.SelfSignature.KeyLifetimeSecs,
		)
		if hasExpiry {
			allExpiryTimes = append(allExpiryTimes, *expiryTime)
		}
	}

	if len(allExpiryTimes) > 0 {
		earliestExpiry := earliest(allExpiryTimes)
		return true, &earliestExpiry
	} else {
		return false, nil
	}
}

// getMostRecentEncryptionSubkey returns the encryption subkey with latest
// (future-most) CreationTime
func getMostRecentEncryptionSubkey(key pgpkey.PgpKey) *openpgp.Subkey {
	var subkeys []openpgp.Subkey

	for _, subkey := range key.Subkeys {
		hasEncryptionFlag := subkey.Sig.FlagEncryptCommunications || subkey.Sig.FlagEncryptStorage

		if subkey.Sig.FlagsValid && hasEncryptionFlag {
			subkeys = append(subkeys, subkey)
		}
	}

	if len(subkeys) == 0 {
		return nil
	}
	sort.Sort(sort.Reverse(ByCreated(subkeys)))
	return &subkeys[0]
}

// ByCreated implements sort.Interface for []openpgp.Subkey based on
// the PrimaryKey.CreationTime field.
type ByCreated []openpgp.Subkey

func (a ByCreated) Len() int      { return len(a) }
func (a ByCreated) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a ByCreated) Less(i, j int) bool {
	iTime := a[i].PublicKey.CreationTime
	jTime := a[j].PublicKey.CreationTime
	return iTime.Before(jTime)
}

// getEarliestExpiryTime returns the soonest expiry time from the key that
// would cause it to lose functionality.
//
// There are 3 types of self-signatures: (https://tools.ietf.org/html/rfc4880#section-5.2.3.3)
//
// 1. certification self-signatures (0x10, 0x11, 0x12, 0x13)
//    * user ID 1 + preferences
//    * user ID 2 + preferences
//
// 2. subkey binding signatures (0x18)
//    * subkey
//
// 3. direct key signatures (0x1F)
//
// * the primary key
// * all subkeys "subkey binding signatures"
// * self signatures / UIDs (?)
//
// There are also *signature expiration times* - the validity period of the
// signature. https://tools.ietf.org/html/rfc4880#section-5.2.3.10
// this is in Signature.SigLifetimeSecs

func getEarliestExpiryTime(key pgpkey.PgpKey) (bool, *time.Time) {
	var allExpiryTimes []time.Time

	for _, id := range key.Identities {
		hasExpiry, expiryTime := calculateExpiry(
			key.PrimaryKey.CreationTime, // not to be confused with the time of the *signature*
			id.SelfSignature.KeyLifetimeSecs,
		)
		if hasExpiry {
			allExpiryTimes = append(allExpiryTimes, *expiryTime)
		}
	}

	for _, subkey := range key.Subkeys {
		hasExpiry, expiryTime := getSubkeyExpiry(subkey)
		if hasExpiry {
			allExpiryTimes = append(allExpiryTimes, *expiryTime)
		}
	}

	if len(allExpiryTimes) > 0 {
		earliestExpiry := earliest(allExpiryTimes)
		return true, &earliestExpiry
	} else {
		return false, nil
	}
}

func earliest(times []time.Time) time.Time {
	if len(times) == 0 {
		panic(fmt.Errorf("earliest called with empty slice"))
	}

	set := false
	var earliestSoFar time.Time

	for _, t := range times {
		if !set || t.Before(earliestSoFar) {
			earliestSoFar = t
			set = true
		}
	}
	return earliestSoFar
}

// calculateExpiry takes a creationtime and a key lifetime in seconds (pointer)
// and returns a corresponding time.Time
//
// From https://tools.ietf.org/html/rfc4880#section-5.2.3.6
// "If this is not present or has a value of zero, the key never expires."
func calculateExpiry(creationTime time.Time, lifetimeSecs *uint32) (bool, *time.Time) {
	//
	if lifetimeSecs == nil {
		return false, nil
	}

	if *lifetimeSecs == 0 {
		return false, nil
	}

	expiry := creationTime.Add(time.Duration(*lifetimeSecs) * time.Second).In(time.UTC)
	return true, &expiry
}

// nextExpiryTime returns the expiry time in UTC, according to the policy:
//     "30 days after the 1st of the next month"
// for example, if today is 15th September, nextExpiryTime would return
// 1st October + 30 days
func nextExpiryTime(now time.Time) time.Time {
	return firstOfNextMonth(now).Add(thirtyDays).In(time.UTC)
}

func firstOfNextMonth(today time.Time) time.Time {
	firstOfThisMonth := beginningOfMonth(today)
	return beginningOfMonth(firstOfThisMonth.Add(fortyFiveDays))
}

func beginningOfMonth(now time.Time) time.Time {
	y, m, _ := now.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, now.Location()).In(time.UTC)
}