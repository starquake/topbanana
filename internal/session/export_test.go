package session

import (
	"encoding/base64"
	"strconv"
)

// ExportNewWithClock exposes newWithClock so external _test packages
// can build a Manager whose clock returns a fixed value.
var ExportNewWithClock = newWithClock

// ExportEncodeLegacy emits the pre-#112 PR3 two-field session cookie
// (playerID|issuedAt) signed with the given key. Lets the
// backwards-compat decode test mint a legacy cookie without exposing
// the internal encode helper to the production package surface.
func ExportEncodeLegacy(playerID, issuedAt int64, key []byte) string {
	payload := strconv.FormatInt(playerID, integerBase) + "|" +
		strconv.FormatInt(issuedAt, integerBase)
	payloadPart := base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := sign([]byte(payload), key)
	macPart := base64.RawURLEncoding.EncodeToString(mac)

	return payloadPart + "." + macPart
}
