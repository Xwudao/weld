package configcheck

import (
	"strings"
	"testing"
)

const reference = `environment: development
http:
  addr: ":8080"
  read_timeout_seconds: 5
database:
  password: ""
  port: 5432
rate_limit:
  enabled: true
  requests_per_second: 10
  trusted_proxies: []
cors:
  allowed_origins: ["http://localhost:5173"]
labels: {}
extra:
`

// render joins findings as "level key" lines, the part a test asserts on.
func render(findings []Finding) string {
	lines := make([]string, len(findings))
	for i, f := range findings {
		lines[i] = string(f.Level) + " " + f.Key
	}
	return strings.Join(lines, "\n")
}

func TestCheckAcceptsTheReferenceItself(t *testing.T) {
	findings, err := Check([]byte(reference), []byte(reference))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings for an identical file:\n%s", render(findings))
	}
}

func TestCheckReportsUnknownMissingAndUndecodableSettings(t *testing.T) {
	config := `environment: production
http:
  addr: 9090
  read_timeout_seconds: "5"
database:
  password: hunter2-sentinel
  port: 5432
rate_limit:
  enabled: "yes"
  requests_per_second: 2.5
  burts: 40
  trusted_proxies: 10.0.0.0/8
cors:
  allowed_origins: ["https://a.example", {host: b}]
labels:
  team: core
extra:
  anything: [1, 2]
`
	findings, err := Check([]byte(config), []byte(reference))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"fail cors.allowed_origins[1]",
		"fail http.read_timeout_seconds",
		"warn rate_limit.burts",
		"fail rate_limit.enabled",
		"warn rate_limit.requests_per_second",
		"fail rate_limit.trusted_proxies",
	}, "\n")
	if got := render(findings); got != want {
		t.Fatalf("findings:\n%s\nwant:\n%s", got, want)
	}
	for _, f := range findings {
		if strings.Contains(f.Message, "hunter2") || strings.Contains(f.Message, "10.0.0.0") || strings.Contains(f.Message, "yes") {
			t.Fatalf("a finding quotes a value: %+v", f)
		}
	}
}

func TestCheckReportsMissingSections(t *testing.T) {
	findings, err := Check([]byte("http:\n  addr: \":1\"\n"), []byte(reference))
	if err != nil {
		t.Fatal(err)
	}
	got := render(findings)
	for _, want := range []string{"warn database", "warn http.read_timeout_seconds", "warn rate_limit"} {
		if !strings.Contains(got, want) {
			t.Fatalf("findings lack %q:\n%s", want, got)
		}
	}
	empty, err := Check(nil, []byte(reference))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(render(empty), "warn environment") {
		t.Fatalf("an empty config.yml does not report its missing sections:\n%s", render(empty))
	}
}

func TestCheckFollowsAnchorsAndMergeKeys(t *testing.T) {
	config := `base: &base
  port: 5432
  password: ""
database:
  <<: *base
environment: development
http: {addr: ":1", read_timeout_seconds: 1}
rate_limit: {enabled: true, requests_per_second: 1, trusted_proxies: []}
cors: {allowed_origins: []}
labels: {}
extra:
`
	findings, err := Check([]byte(config), []byte(reference))
	if err != nil {
		t.Fatal(err)
	}
	if got := render(findings); got != "warn base" {
		t.Fatalf("findings:\n%s\nwant only the unknown anchor holder", got)
	}
}

func TestCheckRefusesInvalidYAMLWithoutQuotingIt(t *testing.T) {
	_, err := Check([]byte("password: [unterminated-sentinel\n"), []byte(reference))
	if err == nil || strings.Contains(err.Error(), "sentinel") {
		t.Fatalf("Check(invalid) = %v, want an error that does not quote the input", err)
	}
}

func TestCheckRefusesDuplicateKeys(t *testing.T) {
	config := reference + "http:\n  addr: \":2\"\n"
	findings, err := Check([]byte(config), []byte(reference))
	if err != nil {
		t.Fatal(err)
	}
	if got := render(findings); !strings.Contains(got, "fail http") {
		t.Fatalf("findings:\n%s\nwant the duplicate http key", got)
	}
}
