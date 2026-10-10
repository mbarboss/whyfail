// Package redact removes secrets from captured command output before it is
// sent to a model.
//
// Rules favor recall over precision: losing a word of context costs less than
// sending a credential to the model. Token formats follow the gitleaks
// default ruleset with looser length bounds.
package redact

import (
	"regexp"
	"slices"
	"strings"
)

// Kind names a type of secret. It appears in placeholders such as
// "[REDACTED:github-token]".
type Kind string

// Kinds of secrets, in the order their rules run.
const (
	PrivateKey     Kind = "private-key"
	URLCredentials Kind = "url-credentials"
	Authorization  Kind = "authorization"
	GitHubToken    Kind = "github-token"
	GitLabToken    Kind = "gitlab-token"
	AWSAccessKey   Kind = "aws-access-key"
	AWSSecretKey   Kind = "aws-secret-key"
	JWT            Kind = "jwt"
	SlackToken     Kind = "slack-token"
	StripeKey      Kind = "stripe-key"
	GoogleAPIKey   Kind = "google-api-key"
	NPMToken       Kind = "npm-token"
	PyPIToken      Kind = "pypi-token"
	APIKey         Kind = "api-key"
	Credential     Kind = "credential"
)

const placeholderPrefix = "[REDACTED:"

func placeholder(k Kind) string { return placeholderPrefix + string(k) + "]" }

// Result is redacted text plus how many secrets of each kind were removed.
type Result struct {
	Text   string
	Counts map[Kind]int
}

// Total returns the number of secrets removed.
func (r Result) Total() int {
	n := 0
	for _, c := range r.Counts {
		n += c
	}
	return n
}

// Kinds returns the kinds removed, sorted.
func (r Result) Kinds() []Kind {
	if len(r.Counts) == 0 {
		return nil
	}
	kinds := make([]Kind, 0, len(r.Counts))
	for k := range r.Counts {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	return kinds
}

// rule replaces matches of re. Groups named "keep" are kept in place and the
// group named "secret" is replaced; without a "secret" group the whole match
// is replaced.
type rule struct {
	kind Kind
	re   *regexp.Regexp
}

const pemLabel = `[ A-Z0-9_-]{0,100}PRIVATE KEY(?: BLOCK)?-----`

// rules run in order, most specific first, so a token inside a credential pair
// is reported by its own kind.
var rules = []rule{
	// Whole blocks, including one whose end is missing. Then an END left
	// without its BEGIN because the tail cut it: the body lines just above it.
	{PrivateKey, regexp.MustCompile(`(?s)-----BEGIN` + pemLabel + `.*?(?:-----END` + pemLabel + `|\z)`)},
	{PrivateKey, regexp.MustCompile(`(?m)(?:^(?:[A-Za-z0-9+/=]{8,}|(?:Proc-Type|DEK-Info|Comment):[^\n]*)?\r?\n)*-----END` + pemLabel)},

	{URLCredentials, regexp.MustCompile(`(?i)(?P<keep>\b[a-z][a-z0-9+.-]{0,30}://[^\s:@/]+:)(?P<secret>[^\s@/]+)@`)},
	// A token used as the URL user, as in https://<token>@github.com.
	{URLCredentials, regexp.MustCompile(`(?i)(?P<keep>\b[a-z][a-z0-9+.-]{0,30}://)(?P<secret>[A-Za-z0-9_.~-]{16,})@`)},

	{Authorization, regexp.MustCompile(`(?i)(?P<keep>\b(?:proxy-)?authorization["']?\s*[:=]\s*["']?(?:(?:bearer|basic|token|digest|bot)\s+)?)(?P<secret>[^\s"',;]+)`)},
	{Authorization, regexp.MustCompile(`(?i)(?P<keep>\bbearer\s+)(?P<secret>[A-Za-z0-9._~+/-]{8,}=*)`)},

	{GitHubToken, regexp.MustCompile(`gh[pousr]_[0-9A-Za-z]{30,}|github_pat_\w{40,}`)},
	{GitLabToken, regexp.MustCompile(`gl(?:pat|dt|rt|cbt|ptt|ft|soat|oas|agent|imt|ffct)-[\w.-]{20,}|GR1348941[\w-]{20,}`)},
	{AWSAccessKey, regexp.MustCompile(`\b(?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z2-7]{16}\b`)},
	{AWSSecretKey, regexp.MustCompile(`(?i)(?P<keep>(?:aws_?)?secret_?(?:access_?)?key["']?\s*[:=]\s*["']?)(?P<secret>[A-Za-z0-9/+=]{40})`)},
	{JWT, regexp.MustCompile(`\bey[A-Za-z0-9_-]{10,}\.ey[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]*`)},
	// Webhook URLs keep their host; the path after /services/ is the secret.
	{SlackToken, regexp.MustCompile(`/services/(?P<secret>T[A-Z0-9]+/B[A-Z0-9]+/[A-Za-z0-9]{20,})`)},
	{SlackToken, regexp.MustCompile(`xox[abposre]-[A-Za-z0-9-]{10,}|(?i)xapp-\d-[A-Za-z0-9-]{10,}`)},
	{StripeKey, regexp.MustCompile(`\b(?:sk|rk)_(?:test|live|prod)_[A-Za-z0-9]{10,}`)},
	{GoogleAPIKey, regexp.MustCompile(`AIza[\w-]{35}`)},
	{NPMToken, regexp.MustCompile(`(?i)npm_[a-z0-9]{36}`)},
	{PyPIToken, regexp.MustCompile(`pypi-AgE[\w-]{50,}`)},
	{APIKey, regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},

	// key: value with a quoted value, then "=" values up to a separator, then
	// ":" values up to the end of the line (YAML and log style).
	{Credential, regexp.MustCompile(`(?i)(?P<keep>` + credKey + `\s*["']?\s*[:=]\s*["'])(?P<secret>[^"'\n]+)`)},
	{Credential, regexp.MustCompile(`(?i)(?P<keep>` + credKey + `\s*["']?\s*=\s*)(?P<secret>[^\s"',;&]+)`)},
	{Credential, regexp.MustCompile(`(?i)(?P<keep>` + credKey + `\s*["']?\s*:[ \t]*)(?P<secret>[^\s"'][^\n]*)`)},
	{Credential, regexp.MustCompile(`(?i)(?P<keep>\bpwd\s*=\s*)(?P<secret>[^\s;]+)`)},
}

// credKey matches setting names that hold a secret, such as DB_PASSWORD,
// client_secret or _authToken.
const credKey = `[A-Za-z0-9_.-]*(?:password|passwd|passphrase|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|credentials?)` //nolint:gosec // G101: a pattern for secret names, not a secret

// Redact replaces every secret found in text with a typed placeholder.
func Redact(text string) Result {
	counts := map[Kind]int{}
	for _, r := range rules {
		text = r.apply(text, counts)
	}
	if len(counts) == 0 {
		counts = nil
	}
	return Result{Text: text, Counts: counts}
}

func (r rule) apply(text string, counts map[Kind]int) string {
	keep := r.re.SubexpIndex("keep")
	secret := r.re.SubexpIndex("secret")

	var b strings.Builder
	last := 0
	for _, m := range r.re.FindAllStringSubmatchIndex(text, -1) {
		start, end := m[0], m[1]
		if secret >= 0 {
			start, end = m[2*secret], m[2*secret+1]
		}
		value := text[start:end]
		// Already redacted, possibly by an earlier pass: leave it alone so
		// Redact is idempotent and placeholders are not counted twice.
		if strings.HasPrefix(value, placeholderPrefix) || (keep >= 0 && strings.Contains(text[m[2*keep]:m[2*keep+1]], placeholderPrefix)) {
			continue
		}
		b.WriteString(text[last:start])
		b.WriteString(placeholder(r.kind))
		last = end
		counts[r.kind]++
	}
	if last == 0 {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}
