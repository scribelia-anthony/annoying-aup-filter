// Package id generates short, opaque, URL-safe identifiers used to label
// captures and rules. Not cryptographically authenticated — collision space
// is ~2^48 which is plenty for an in-memory ring buffer.
package id

import (
	"crypto/rand"
	"encoding/hex"
)

func New() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
