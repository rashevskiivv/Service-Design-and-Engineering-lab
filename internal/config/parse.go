package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"local-generative-ai/internal/oai"
)

// parser reads variables, records every name it reads (to detect unknown
// ones) and collects all errors so one startup shows every problem.
type parser struct {
	getenv func(string) string
	known  map[string]bool
	errs   []error
}

func (p *parser) get(name string) string {
	p.known[name] = true
	return strings.TrimSpace(p.getenv(name))
}

func (p *parser) fail(name, format string, args ...any) {
	p.errs = append(p.errs, fmt.Errorf("%s %s", name, fmt.Sprintf(format, args...)))
}

func (p *parser) str(name, def string) string {
	if v := p.get(name); v != "" {
		return v
	}
	return def
}

func (p *parser) bool(name string, def bool) bool {
	v := p.get(name)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.fail(name, "must be true or false")
	}
	return b
}

func (p *parser) int(name string, def, minimum int) int {
	v := p.get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < minimum {
		p.fail(name, "must be an integer >= %d", minimum)
		return def
	}
	return n
}

func (p *parser) dur(name string, def, minimum time.Duration) time.Duration {
	v := p.get(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < minimum {
		p.fail(name, "must be a duration >= %s (e.g. 30s, 10m)", minimum)
		return def
	}
	return d
}

func (p *parser) level(name string, def slog.Level) slog.Level {
	v := p.get(name)
	if v == "" {
		return def
	}
	var l slog.Level
	if err := l.UnmarshalText([]byte(v)); err != nil {
		p.fail(name, "must be debug, info, warn or error")
		return def
	}
	return l
}

// secret reads NAME or NAME_FILE (not both) and validates the value when set.
func (p *parser) secret(name string) string {
	v, file := p.get(name), p.get(name+"_FILE")
	if v != "" && file != "" {
		p.fail(name, "and %s_FILE are both set; use one", name)
		return ""
	}
	if file != "" {
		s, err := readSecretFile(file)
		if err != nil {
			p.fail(name+"_FILE", "cannot be read")
			return ""
		}
		v = s
	}
	if v == "" {
		return ""
	}
	if err := ValidateSecret(v); err != nil {
		p.fail(name, "%v", err)
		return ""
	}
	return v
}

func (p *parser) url(name, def string) *url.URL {
	v := p.str(name, def)
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		p.fail(name, "must be an absolute http(s) URL")
		return nil
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u
}

// isControl reports the bytes net/http refuses in a header value: every
// control character except tab (security-code V3).
func isControl(r rune) bool { return (r < 0x20 && r != '\t') || r == 0x7f }

var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// headersReserved are set by the gateway itself and cannot be overridden.
var headersReserved = map[string]bool{
	"Host": true, "Content-Length": true, "Content-Type": true, "Accept": true, "Accept-Encoding": true,
	"Connection": true, "Transfer-Encoding": true, "Te": true, "Trailer": true, "Upgrade": true,
	"X-Request-Id": true, "User-Agent": true,
}

// headers parses Name=value;Name2=value2 (a secret: read from NAME or NAME_FILE,
// never logged). Authorization is allowed only when LGAI_UPSTREAM_API_KEY is unset.
func (p *parser) headers(name string, haveAPIKey bool) http.Header {
	v, file := p.get(name), p.get(name+"_FILE")
	if v != "" && file != "" {
		p.fail(name, "and %s_FILE are both set; use one", name)
		return nil
	}
	if file != "" {
		s, err := readSecretFile(file)
		if err != nil {
			p.fail(name+"_FILE", "cannot be read")
			return nil
		}
		v = strings.TrimSpace(s)
	}
	if v == "" {
		return nil
	}
	h := http.Header{}
	for i, item := range strings.Split(v, ";") {
		if strings.TrimSpace(item) == "" {
			continue
		}
		k, val, ok := strings.Cut(item, "=")
		k, val = strings.TrimSpace(k), strings.TrimSpace(val)
		key := http.CanonicalHeaderKey(k)
		switch {
		case !ok || !headerName.MatchString(k) || val == "":
			p.fail(name, "entry %d must be Name=value", i+1) // the value is never echoed
		case strings.IndexFunc(val, isControl) >= 0:
			p.fail(name, "entry %d contains a control character; only printable text and tabs are allowed", i+1)
		case headersReserved[key]:
			p.fail(name, "cannot set %s", key)
		case key == "Authorization" && haveAPIKey:
			p.fail(name, "sets Authorization while LGAI_UPSTREAM_API_KEY is also set")
		default:
			h.Set(key, val)
		}
	}
	return h
}

var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,64}$`)

// models parses LGAI_MODELS ("public=upstream,…" or "name") and attaches the
// per-alias LGAI_MODEL_DEFAULTS ("alias={json};alias2={json}").
func (p *parser) models(name, defaultsName string) []oai.Model {
	v := p.get(name)
	if v == "" {
		p.fail(name, "is required, e.g. coder=gpt-oss:20b")
		return []oai.Model{{}}
	}
	var out []oai.Model
	seen := map[string]bool{}
	for _, item := range strings.Split(v, ",") {
		pub, up, found := strings.Cut(strings.TrimSpace(item), "=")
		pub, up = strings.TrimSpace(pub), strings.TrimSpace(up)
		if !found {
			up = pub
		}
		if !aliasPattern.MatchString(pub) || up == "" {
			p.fail(name, "entry %q must be public=upstream with a 1-64 char alias", item)
			continue
		}
		if seen[pub] {
			p.fail(name, "lists alias %q twice", pub)
			continue
		}
		seen[pub] = true
		out = append(out, oai.Model{Alias: pub, Upstream: up})
	}
	if len(out) == 0 {
		return []oai.Model{{}}
	}
	defaults := p.modelDefaults(defaultsName)
	for alias := range defaults {
		if !seen[alias] {
			p.fail(defaultsName, "names alias %q, which is not in %s", alias, name)
		}
	}
	for i := range out {
		out[i].Defaults = defaults[out[i].Alias]
	}
	return out
}

func (p *parser) modelDefaults(name string) map[string]oai.ModelDefaults {
	out := map[string]oai.ModelDefaults{}
	rest := p.get(name)
	for rest != "" {
		alias, after, ok := strings.Cut(rest, "=")
		alias = strings.TrimSpace(alias)
		if !ok || alias == "" {
			p.fail(name, "must be alias={json}[;alias={json}]")
			return out
		}
		dec := json.NewDecoder(strings.NewReader(after))
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			p.fail(name, "has invalid JSON for alias %q", alias)
			return out
		}
		d, err := oai.ParseModelDefaults(raw)
		if err != nil {
			p.fail(name, "for alias %q: %v", alias, err)
			return out
		}
		out[alias] = d
		rest = strings.TrimSpace(after[dec.InputOffset():])
		if rest != "" && !strings.HasPrefix(rest, ";") {
			p.fail(name, "entries must be separated by ';'")
			return out
		}
		rest = strings.TrimSpace(strings.TrimPrefix(rest, ";"))
	}
	return out
}
