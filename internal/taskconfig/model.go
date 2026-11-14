// Package taskconfig describes persisted tasks without UI, filesystem or execution state.
// Decode/Encode check the legacy wire shape; Validate checks domain constraints and
// reports checks this slice cannot perform. None of these APIs authorize execution.
package taskconfig

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type TaskKind string

const (
	Sync   TaskKind = "Sync"
	Moving TaskKind = "Move"
)

type Strategy string

const (
	Mirror  Strategy = "Mirror"
	AddOnly Strategy = "AddOnly"
	Move    Strategy = "Move"
)

type DeleteMode string

const (
	Recycle   DeleteMode = "Recycle"
	Permanent DeleteMode = "Permanent"
)

type CollisionPolicy string

const (
	Skip   CollisionPolicy = "Skip"
	Suffix CollisionPolicy = "Suffix"
)

// IDs are canonical UUID strings. Zero UUIDs are legal legacy IDs; uniqueness is
// a list validation concern, not an excuse to regenerate an existing identity.
type ID string

func NewID() ID {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return ID(fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]))
}
func parseID(s string) (ID, error) {
	s = strings.TrimPrefix(s, "urn:uuid:")
	if len(s) == 38 && s[0] == '{' && s[37] == '}' {
		s = s[1:37]
	}
	if len(s) == 36 {
		if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return "", fmt.Errorf("invalid UUID")
		}
		s = s[:8] + s[9:13] + s[14:18] + s[19:23] + s[24:]
	}
	if len(s) != 32 {
		return "", fmt.Errorf("invalid UUID")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("invalid UUID: %w", err)
	}
	return ID(fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])), nil
}

type extras map[string]json.RawMessage
type Task struct {
	Name         string
	SourcePath   string
	Trigger      Trigger
	Destinations []Destination
	ID           ID `json:"Id"`
	Kind         TaskKind
	extra        extras
}
type Destination struct {
	Name                string
	Target              Target
	Filters             []Rule
	Strategy            Strategy
	ID                  ID `json:"Id"`
	VerifyContents      bool
	DeleteMode          DeleteMode
	FlattenStructure    bool
	CollisionPolicy     CollisionPolicy
	MassDeleteThreshold float64
	extra               extras
}
type Target struct {
	Kind  string `json:"kind"`
	Path  string
	extra extras
}

// HasUnknownFields reports preserved members whose meaning is not implemented.
// These methods inspect this object only; consumers must check nested objects.
func (t Task) HasUnknownFields() bool        { return len(t.extra) != 0 }
func (d Destination) HasUnknownFields() bool { return len(d.extra) != 0 }
func (t Target) HasUnknownFields() bool      { return len(t.extra) != 0 }
func (t Trigger) HasUnknownFields() bool     { return len(t.extra) != 0 }

// Trigger/Rule use explicit discriminators. The codec and Validate reject
// incompatible payloads created in Go; no implicit nil/bool lifecycle exists.
type Trigger struct {
	Kind           string  `json:"kind"`
	CronExpression *string `json:",omitempty"`
	SettleSeconds  *int32  `json:",omitempty"`
	extra          extras
}

func (t Trigger) SettleWindow() (time.Duration, bool) {
	if t.Kind != "watch" || t.SettleSeconds == nil {
		return 0, false
	}
	n := max(int32(1), min(int32(600), *t.SettleSeconds))
	return time.Duration(n) * time.Second, true
}

type Rule struct {
	Kind      string  `json:"kind"`
	Prefix    *string `json:",omitempty"`
	Extension *string `json:",omitempty"`
	Pattern   *string `json:",omitempty"`
	Rules     *[]Rule `json:",omitempty"`
	Rule      *Rule   `json:",omitempty"`
	extra     extras
	// Preserve the original spelling only where legacy one-pass extension
	// normalization is non-idempotent. Marshal verifies it against the current
	// canonical value so editing a public field cannot resurrect stale spelling.
	extensionWire string
}
