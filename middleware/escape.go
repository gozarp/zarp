// Copyright 2026 Subhanjan Adhikary. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package middleware

const hexDigits = "0123456789abcdef"

// appendEscaped appends s with control characters escaped, so one log entry can
// only ever be one line.
//
// The request path needs this and the other logged fields do not. When a route
// matches, Entry.Path is the registered pattern — a string the program wrote.
// When none matches, the logger falls back to the requested path, and net/http
// hands that over URL-decoded: "%0A" in the request line arrives as a real
// newline. Written straight out, an attacker picks what the next line of your
// log says, and a forged entry is indistinguishable from a genuine one. Method
// is a token net/http has already validated, and ClientIP is either a peer
// address or a string that parsed as an IP.
//
// It appends rather than building a string, so the text sink stays
// allocation-free. Bytes above 0x7f pass through: they cannot break a line, and
// mangling them would corrupt every non-ASCII path.
func appendEscaped(b []byte, s string) []byte {
	for i := range len(s) {
		switch c := s[i]; {
		case c == '\n':
			b = append(b, '\\', 'n')
		case c == '\r':
			b = append(b, '\\', 'r')
		case c == '\t':
			b = append(b, '\\', 't')
		case c < 0x20 || c == 0x7f:
			b = append(b, '\\', 'x', hexDigits[c>>4], hexDigits[c&0x0f])
		default:
			b = append(b, c)
		}
	}
	return b
}

// escaped is appendEscaped for a caller that needs a string. It allocates, so
// it belongs on paths that are already exceptional — recovering from a panic,
// say.
func escaped(s string) string {
	return string(appendEscaped(make([]byte, 0, len(s)), s))
}
