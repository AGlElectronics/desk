// Code generated from con-services shared/go/authbase; DO NOT EDIT. Run scripts/sync-shared.

package authbase

import "strings"

// DevIssuer is the issuer of people signed in with AUTH_MODE=dev.
const DevIssuer = "dev"

// UserKey identifies a person across Con Services: the token's issuer (iss)
// and subject (sub). Two people with the same subject at different issuers
// are different people. Compare keys with ==.
type UserKey struct {
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
}

// IsZero reports a key without issuer or subject.
func (k UserKey) IsZero() bool { return k.Issuer == "" || k.Subject == "" }

// String is "<issuer>|<subject>", for logs and map keys. Issuers are URLs
// and never contain '|'.
func (k UserKey) String() string { return k.Issuer + "|" + k.Subject }

// ParseUserKey is the inverse of UserKey.String.
func ParseUserKey(s string) (UserKey, bool) {
	i := strings.Index(s, "|")
	if i <= 0 || i == len(s)-1 {
		return UserKey{}, false
	}
	return UserKey{Issuer: s[:i], Subject: s[i+1:]}, true
}
