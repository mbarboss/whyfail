package redact

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// fake builds a token-shaped string at run time so that the secret scanner
// in the pre-commit hook does not flag test fixtures.
func fake(prefix string, n int) string {
	const alphabet = "Ab3dE5gH7jK9mN2pQ4sT6vW8yZ"
	var b strings.Builder
	b.WriteString(prefix)
	for i := range n {
		b.WriteByte(alphabet[i%len(alphabet)])
	}
	return b.String()
}

func pemBlock(label string) string {
	return "-----BEGIN " + label + "-----\n" +
		fake("MIIEow", 58) + "\n" + fake("q", 63) + "\n" +
		"-----END " + label + "-----"
}

var (
	ghp     = fake("ghp_", 36)
	jwt     = "eyJ" + fake("", 30) + ".eyJ" + fake("", 40) + "." + fake("", 43)
	awsKey  = "AKIA" + "IOSFODNN7EXAMPLE"
	awsSec  = fake("wJalrXUtnFEMI/K7MDENG/", 18)
	slack   = "xoxb-" + "1234567890123-1234567890123-" + fake("", 24)
	stripe  = "sk_live_" + fake("", 24)
	google  = "AIza" + fake("", 35)
	npmTok  = "npm_" + fake("", 36)
	pypiTok = "pypi-AgEIcHlwaS5vcmc" + fake("", 60)
	llmKey  = "sk-proj-" + fake("", 48)
	// base64 of "user:password"
	basicCred = "dXNlcjpw" + "YXNzd29yZA=="
)

func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		want     string
		wantKind Kind
	}{
		{
			"RSA private key block",
			"loaded key:\n" + pemBlock("RSA PRIVATE KEY") + "\nerror: bad passphrase",
			"loaded key:\n[REDACTED:private-key]\nerror: bad passphrase",
			PrivateKey,
		},
		{
			"OpenSSH private key block",
			pemBlock("OPENSSH PRIVATE KEY"),
			"[REDACTED:private-key]",
			PrivateKey,
		},
		{
			"PGP private key block",
			pemBlock("PGP PRIVATE KEY BLOCK"),
			"[REDACTED:private-key]",
			PrivateKey,
		},
		{
			"key block cut at the start by the tail",
			fake("q", 64) + "\n-----END PRIVATE KEY-----\nssh: handshake failed",
			"[REDACTED:private-key]\nssh: handshake failed",
			PrivateKey,
		},
		{
			"key block cut at the start after a dashed separator",
			"----- test output -----\n" + fake("q", 64) + "\n" + fake("w", 40) + "==\n-----END RSA PRIVATE KEY-----\ndone",
			"----- test output -----\n[REDACTED:private-key]\ndone",
			PrivateKey,
		},
		{
			"key block without end",
			"key:\n-----BEGIN EC PRIVATE KEY-----\n" + fake("M", 64),
			"key:\n[REDACTED:private-key]",
			PrivateKey,
		},
		{
			"postgres URL credentials",
			"connect postgres://app:s3cr3t-pw@db.internal:5432/app failed",
			"connect postgres://app:[REDACTED:url-credentials]@db.internal:5432/app failed",
			URLCredentials,
		},
		{
			"mongodb+srv URL credentials",
			"mongodb+srv://admin:p%40ss@cluster0.example.net/test",
			"mongodb+srv://admin:[REDACTED:url-credentials]@cluster0.example.net/test",
			URLCredentials,
		},
		{
			"token as URL user",
			"fatal: unable to access 'https://" + ghp + "@github.com/o/r.git/'",
			"fatal: unable to access 'https://[REDACTED:url-credentials]@github.com/o/r.git/'",
			URLCredentials,
		},
		{
			"bearer header",
			"> Authorization: Bearer " + jwt,
			"> Authorization: Bearer [REDACTED:authorization]",
			Authorization,
		},
		{
			"basic header lowercase",
			`curl -H "authorization: basic ` + basicCred + `"`,
			`curl -H "authorization: basic [REDACTED:authorization]"`,
			Authorization,
		},
		{
			"proxy authorization header",
			"Proxy-Authorization: Basic " + basicCred,
			"Proxy-Authorization: Basic [REDACTED:authorization]",
			Authorization,
		},
		{
			"bare bearer token",
			"using Bearer abcdef1234567890abcdef",
			"using Bearer [REDACTED:authorization]",
			Authorization,
		},
		{"GitHub classic token", "token " + ghp + " rejected", "token [REDACTED:github-token] rejected", GitHubToken},
		{"GitHub OAuth token", fake("gho_", 36), "[REDACTED:github-token]", GitHubToken},
		{"GitHub app token", fake("ghs_", 36), "[REDACTED:github-token]", GitHubToken},
		{"GitHub fine-grained token", fake("github_pat_", 82), "[REDACTED:github-token]", GitHubToken},
		{"GitLab PAT", "remote: " + fake("glpat-", 20), "remote: [REDACTED:gitlab-token]", GitLabToken},
		{"GitLab routable PAT", fake("glpat-", 40) + ".01.abcdefghi", "[REDACTED:gitlab-token]", GitLabToken},
		{"GitLab runner token", fake("glrt-", 20), "[REDACTED:gitlab-token]", GitLabToken},
		{"GitLab deploy token", fake("gldt-", 20), "[REDACTED:gitlab-token]", GitLabToken},
		{"AWS access key", "AccessKeyId: " + awsKey, "AccessKeyId: [REDACTED:aws-access-key]", AWSAccessKey},
		{
			"AWS secret key",
			"aws_secret_access_key = " + awsSec,
			"aws_secret_access_key = [REDACTED:aws-secret-key]",
			AWSSecretKey,
		},
		{"JWT", "token rejected: " + jwt + " (expired)", "token rejected: [REDACTED:jwt] (expired)", JWT},
		{"Slack bot token", slack, "[REDACTED:slack-token]", SlackToken},
		{
			"Slack webhook",
			"POST https://hooks.slack.com/services/T000/B000/" + fake("", 24),
			"POST https://hooks.slack.com/services/[REDACTED:slack-token]",
			SlackToken,
		},
		{"Stripe key", "Invalid API Key provided: " + stripe, "Invalid API Key provided: [REDACTED:stripe-key]", StripeKey},
		{"Google API key", "key=" + google, "key=[REDACTED:google-api-key]", GoogleAPIKey},
		{"npm token", "//registry.npmjs.org/:_authToken=" + npmTok, "//registry.npmjs.org/:_authToken=[REDACTED:npm-token]", NPMToken},
		{"PyPI token", "password: " + pypiTok, "password: [REDACTED:pypi-token]", PyPIToken},
		{"LLM API key", "Incorrect API key provided: " + llmKey, "Incorrect API key provided: [REDACTED:api-key]", APIKey},
		{"password pair", "password=hunter2 user=bob", "password=[REDACTED:credential] user=bob", Credential},
		{"YAML secret", "  client_secret: abc123xyz", "  client_secret: [REDACTED:credential]", Credential},
		{"JSON password", `{"user":"bob","password":"hunter2"}`, `{"user":"bob","password":"[REDACTED:credential]"}`, Credential},
		{"quoted value with spaces", `DB_PASSWORD="correct horse battery"`, `DB_PASSWORD="[REDACTED:credential]"`, Credential},
		{"env export", "export API_KEY=abc123", "export API_KEY=[REDACTED:credential]", Credential},
		{"access token pair", "access_token: abcdef", "access_token: [REDACTED:credential]", Credential},
		{
			"ADO.NET connection string",
			"Server=db;Database=app;User Id=sa;Password=Str0ng!Pass;",
			"Server=db;Database=app;User Id=sa;Password=[REDACTED:credential];",
			Credential,
		},
		{"ODBC Pwd", "Driver={SQL Server};Uid=sa;Pwd=Str0ng!Pass;", "Driver={SQL Server};Uid=sa;Pwd=[REDACTED:credential];", Credential},
		{"passphrase", "passphrase: open sesame", "passphrase: [REDACTED:credential]", Credential},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.in)

			if got.Text != tt.want {
				t.Errorf("Text =\n%q\nwant\n%q", got.Text, tt.want)
			}
			if got.Counts[tt.wantKind] == 0 {
				t.Errorf("Counts = %v, want %s counted", got.Counts, tt.wantKind)
			}
		})
	}
}

func TestRedactLeavesOrdinaryOutputAlone(t *testing.T) {
	ordinary := []string{
		"commit 3f2a9c1d8e7b6a5f4e3d2c1b0a9f8e7d6c5b4a3f",
		"request id 123e4567-e89b-12d3-a456-426614174000",
		"fatal: unable to access 'https://github.com/o/r.git/': Could not resolve host",
		"HTTP/1.1 401 Authorization Required",
		"error: authorization failed for user bob",
		"options: max_tokens=512 temperature=0",
		"open /home/dev/.ssh/id_ed25519: permission denied",
		"npm ERR! code E401 Incorrect or missing password.",
		"Enter password:",
		"tokenizer: unexpected EOF",
		"secrets.go:12:3: undefined: loadSecrets",
		"ssh-keygen -t ed25519 -C dev@example.com",
		"see https://docs.example.com/auth?lang=en#tokens",
		"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----",
		"ghp_short is not a token",
	}
	for _, in := range ordinary {
		t.Run(in, func(t *testing.T) {
			got := Redact(in)

			if got.Text != in || got.Total() != 0 {
				t.Errorf("Redact changed ordinary output: %q (counts %v)", got.Text, got.Counts)
			}
		})
	}
}

func TestRedactCountsEverySecret(t *testing.T) {
	in := "a " + ghp + "\nb " + fake("gho_", 36) + "\npassword=x\nhttps://u:p@h.test"

	got := Redact(in)

	if got.Total() != 4 || got.Counts[GitHubToken] != 2 || got.Counts[Credential] != 1 || got.Counts[URLCredentials] != 1 {
		t.Errorf("Counts = %v, Total = %d", got.Counts, got.Total())
	}
	if want := []Kind{Credential, GitHubToken, URLCredentials}; !slices.Equal(got.Kinds(), want) {
		t.Errorf("Kinds = %v, want %v", got.Kinds(), want)
	}
}

func TestRedactIsIdempotent(t *testing.T) {
	in := strings.Join([]string{
		pemBlock("RSA PRIVATE KEY"), "https://u:p@h.test", "Authorization: Bearer " + jwt,
		ghp, "password=x", `"secret": "y"`, awsKey, stripe, llmKey,
	}, "\n")

	once := Redact(in)
	twice := Redact(once.Text)

	if twice.Text != once.Text {
		t.Errorf("second pass changed text:\n%q\n%q", once.Text, twice.Text)
	}
}

func TestRedactAdjacentSecrets(t *testing.T) {
	got := Redact(ghp + "," + fake("gho_", 36) + ";" + awsKey)

	if strings.Contains(got.Text, "ghp_") || strings.Contains(got.Text, "gho_") || strings.Contains(got.Text, "AKIA") {
		t.Errorf("secret survived: %q", got.Text)
	}
}

func TestRedactHostileInput(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"binary data", "\x00\x01\xff\xfe" + ghp + "\x00\x7f"},
		{"invalid UTF-8 around secret", "\xc3\x28password=hunter2\xa0\xa1"},
		{"forged placeholder", "[REDACTED:github-token] " + ghp},
		{"secret inside ANSI colors", "\x1b[31m" + ghp + "\x1b[0m"},
		{"key with CRLF", strings.ReplaceAll(pemBlock("RSA PRIVATE KEY"), "\n", "\r\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.in)

			for _, secret := range []string{ghp, "hunter2", "MIIEow"} {
				if strings.Contains(got.Text, secret) {
					t.Errorf("secret %q survived in %q", secret, got.Text)
				}
			}
		})
	}
}

func TestRedactVeryLongLineIsFast(t *testing.T) {
	// 1 MiB without a newline: far beyond what capture keeps, so this bounds
	// the worst case.
	in := strings.Repeat("a", 1<<20) + " password=hunter2 " + strings.Repeat("=", 1<<10)

	start := time.Now()
	got := Redact(in)

	if strings.Contains(got.Text, "hunter2") {
		t.Error("secret survived")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v on a 1 MiB line", d)
	}
}

func TestRedactEmpty(t *testing.T) {
	got := Redact("")

	if got.Text != "" || got.Total() != 0 || got.Kinds() != nil {
		t.Errorf("got %+v", got)
	}
}
