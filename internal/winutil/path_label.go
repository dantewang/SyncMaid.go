package winutil

import "strings"

// PathLabel is a human-facing label, NEVER an operation-path
// normalizer. Extended paths can have semantics (length, trailing dots/spaces)
// that ordinary Win32 callers do not preserve. Keep the original for I/O.
// Unknown device namespaces are left intact rather than guessed.
func PathLabel(native string) string {
	if strings.HasPrefix(strings.ToUpper(native), `\\?\UNC\`) {
		return `\\` + native[len(`\\?\UNC\`):]
	}
	if len(native) >= 7 && strings.HasPrefix(native, `\\?\`) &&
		((native[4] >= 'A' && native[4] <= 'Z') || (native[4] >= 'a' && native[4] <= 'z')) &&
		native[5] == ':' && native[6] == '\\' {
		return native[4:]
	}
	return native
}
