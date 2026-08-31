// NextSQL Identity Policy (`NSIP`).
//
// An identity policy is the operator-authored rulebook an external-identity
// broker consults to turn a verified set of IdP claims into a native NextSQL
// principal and a no-escalation role set. It is pure and offline: parsing,
// validation, subject matching, group mapping, and RBAC intersection happen
// here with no network and no dependency on the SQL engine. A broker (a later
// increment) is expected to load it, and the `nextsqld` authentication path
// never sees it — an identity that clears the policy is still admitted only as
// an ordinary `NSSC1.` short-lived credential.
//
// Wire form: "NSIP" || version(2) || body. The body is a deterministic
// little-endian encoding (see encodeIdentityPolicy). Decoding never allocates
// from an unchecked length and rejects anything malformed with a typed error.
// The file is written mode 0600 with an atomic rename, versioned and
// corruption-validated exactly like the sibling `NSTK`/`NSTR` files; Reload
// keeps the last known-good policy when a new file fails to parse or validate.
package auth

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

const (
	idpMagic   = "NSIP"
	idpVersion = 1

	maxIDPBlob         = 1 << 16 // 64 KiB on disk
	maxSubjectRules    = 64
	maxRuleConds       = 16
	maxGroupMappings   = 256
	maxRuleTransforms  = 8
	maxDefaultRoles    = maxTokenRoles // 16
	maxMappingRoles    = maxTokenRoles // 16
	maxClaimNameLen    = 128
	maxMatchValueLen   = 512
	maxRuleIDLen       = 64
	maxIssuerLen       = 512
	maxPrincipalOut    = maxTokenPrincipal // 128
	maxGroupsPerToken  = 256               // token group values inspected per Map
	maxGroupPatternLen = 512
)

// MatchOp is the comparison a subject-rule condition applies to a claim value.
type MatchOp uint8

const (
	OpEquals    MatchOp = iota + 1 // claim value equals Value exactly
	OpHasPrefix                    // claim value begins with Value
	OpHasSuffix                    // claim value ends with Value
	OpRegex                        // claim value fully matches the RE2 pattern Value (auto-anchored)
)

func (o MatchOp) valid() bool { return o >= OpEquals && o <= OpRegex }

// TransformOp is a pure string transform applied to a derived principal.
type TransformOp uint8

const (
	TransformLower   TransformOp = iota + 1 // ASCII/Unicode lowercase
	TransformBefore                         // keep the text before the first occurrence of A
	TransformAfter                          // keep the text after the first occurrence of A
	TransformReplace                        // replace every occurrence of A with B
)

func (o TransformOp) valid() bool { return o >= TransformLower && o <= TransformReplace }

// MatchCond is one condition on a claim. A subject rule's conditions are ANDed.
type MatchCond struct {
	Claim string // claim name; dotted (e.g. "address.country") walks nested objects
	Op    MatchOp
	Value string
}

// Transform is one step of a principal template pipeline.
type Transform struct {
	Op TransformOp
	A  string
	B  string // TransformReplace only
}

// PrincipalKind selects where a rule's principal text comes from before
// transforms are applied.
type PrincipalKind uint8

const (
	PrincipalLiteral PrincipalKind = iota + 1 // Value is used verbatim
	PrincipalClaim                            // Value names the claim to read
)

func (k PrincipalKind) valid() bool { return k == PrincipalLiteral || k == PrincipalClaim }

// Principal describes how to derive the native login name for a matched rule.
// The result is lowercased and must be 1..128 chars of [a-z0-9._-]; anything
// else fails closed.
type Principal struct {
	Kind       PrincipalKind
	Value      string
	Transforms []Transform
}

// SubjectRule maps a verified subject to a native principal. Rules are ordered
// and the first whose Issuer matches and whose every condition passes wins.
type SubjectRule struct {
	ID        string // operator-assigned, unique within the policy, for audit
	Issuer    string // must equal the verified token issuer exactly
	Match     []MatchCond
	Principal Principal
}

// GroupMapping maps an IdP group to one or more native roles. A literal mapping
// matches a group value exactly. A regex mapping matches with an auto-anchored
// RE2 pattern and may reference capture groups in its role strings as ${1}..${9}
// (${0} is the whole match).
type GroupMapping struct {
	Group   string
	IsRegex bool
	Roles   []string
}

// PolicyDoc is the decoded, operator-authored identity policy.
type PolicyDoc struct {
	SubjectRules  []SubjectRule
	GroupClaim    string // "" disables group mapping; DefaultRoles is then the whole role set
	GroupMappings []GroupMapping
	DefaultRoles  []string // roles used when group mapping yields nothing
}

// MapResult is the outcome of applying a policy to a claim set.
type MapResult struct {
	RuleID    string   // the subject rule that matched
	Principal string   // derived native principal
	Roles     []string // deduped, ordered, <= 16; mapped roles from Map, effective roles from Authorize
}

// IdentityPolicy is a loaded, compiled policy ready to evaluate claim sets.
// It is safe for concurrent use.
type IdentityPolicy struct {
	mu       sync.Mutex
	path     string
	doc      PolicyDoc
	rules    []compiledRule
	mappings []compiledMapping
}

type compiledRule struct {
	rule  SubjectRule
	conds []compiledCond
}

type compiledCond struct {
	cond MatchCond
	re   *regexp.Regexp // non-nil only for OpRegex
}

type compiledMapping struct {
	m  GroupMapping
	re *regexp.Regexp // non-nil only when IsRegex
}

// WriteIdentityPolicy validates doc and writes it to path with an atomic
// rename. An invalid policy is never written.
func WriteIdentityPolicy(path string, doc PolicyDoc) error {
	raw, err := encodeIdentityPolicy(doc)
	if err != nil {
		return err
	}
	return atomicWrite(path, raw)
}

// OpenIdentityPolicy loads and compiles a policy file.
func OpenIdentityPolicy(path string) (*IdentityPolicy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "auth.OpenIdentityPolicy", "read", err)
	}
	doc, err := decodeIdentityPolicy(raw)
	if err != nil {
		return nil, err
	}
	rules, mappings, err := compilePolicy(doc)
	if err != nil {
		return nil, err
	}
	return &IdentityPolicy{path: path, doc: doc, rules: rules, mappings: mappings}, nil
}

// LoadIdentityPolicy compiles an in-memory policy document (no file).
func LoadIdentityPolicy(doc PolicyDoc) (*IdentityPolicy, error) {
	raw, err := encodeIdentityPolicy(doc)
	if err != nil {
		return nil, err
	}
	norm, err := decodeIdentityPolicy(raw)
	if err != nil {
		return nil, err
	}
	rules, mappings, err := compilePolicy(norm)
	if err != nil {
		return nil, err
	}
	return &IdentityPolicy{doc: norm, rules: rules, mappings: mappings}, nil
}

// Path returns the backing file path ("" for an in-memory policy).
func (p *IdentityPolicy) Path() string { return p.path }

// Reload re-reads the policy file. On any read/parse/validate/compile error the
// in-memory policy is left unchanged (last known good).
func (p *IdentityPolicy) Reload() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.path == "" {
		return nerr.New(nerr.InvalidArgument, "auth.IdentityPolicy.Reload", "policy is not attached to a file")
	}
	raw, err := os.ReadFile(p.path)
	if err != nil {
		return nerr.Wrap(nerr.IO, "auth.IdentityPolicy.Reload", "read", err)
	}
	doc, err := decodeIdentityPolicy(raw)
	if err != nil {
		return err
	}
	rules, mappings, err := compilePolicy(doc)
	if err != nil {
		return err
	}
	p.doc, p.rules, p.mappings = doc, rules, mappings
	return nil
}

// Doc returns a deep copy of the compiled policy document.
func (p *IdentityPolicy) Doc() PolicyDoc {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneDoc(p.doc)
}

// PolicySummary is a key-material-free description for listing.
type PolicySummary struct {
	Version       int
	SubjectRules  int
	GroupClaim    string
	GroupMappings int
	DefaultRoles  int
}

// Summary returns counts for operator tooling.
func (p *IdentityPolicy) Summary() PolicySummary {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PolicySummary{
		Version:       idpVersion,
		SubjectRules:  len(p.doc.SubjectRules),
		GroupClaim:    p.doc.GroupClaim,
		GroupMappings: len(p.doc.GroupMappings),
		DefaultRoles:  len(p.doc.DefaultRoles),
	}
}

// Map applies the policy to a verified issuer and claim set. It returns the
// matched rule id, the derived principal, and the mapped roles BEFORE the RBAC
// intersection (for broker-side auditing). Every ambiguous or unmatched input
// is a typed Forbidden error.
func (p *IdentityPolicy) Map(issuer string, claims map[string]any) (MapResult, error) {
	fail := func(msg string) (MapResult, error) {
		return MapResult{}, nerr.New(nerr.Forbidden, "auth.IdentityPolicy", msg)
	}
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		return fail("empty issuer")
	}

	p.mu.Lock()
	rules := p.rules
	mappings := p.mappings
	groupClaim := p.doc.GroupClaim
	defaultRoles := append([]string(nil), p.doc.DefaultRoles...)
	p.mu.Unlock()

	var matched *compiledRule
	for i := range rules {
		if rules[i].rule.Issuer != issuer {
			continue
		}
		if condsPass(rules[i].conds, claims) {
			matched = &rules[i]
			break
		}
	}
	if matched == nil {
		return fail("no subject rule matched")
	}

	principal, err := derivePrincipal(matched.rule.Principal, claims)
	if err != nil {
		return MapResult{}, err
	}

	var roles []string
	if groupClaim == "" {
		roles = append(roles, defaultRoles...)
	} else {
		groups, ok := claimStrings(claims, groupClaim)
		if !ok {
			return fail("group claim is absent")
		}
		if len(groups) > maxGroupsPerToken {
			groups = groups[:maxGroupsPerToken]
		}
		roles = mapGroups(mappings, groups)
		if len(roles) == 0 {
			roles = append(roles, defaultRoles...)
		}
	}

	roles, err = normTokenRoles(roles)
	if err != nil {
		return MapResult{}, nerr.New(nerr.Forbidden, "auth.IdentityPolicy", "mapped role set is invalid")
	}
	if len(roles) == 0 {
		return fail("policy mapped no roles for this identity")
	}
	return MapResult{RuleID: matched.rule.ID, Principal: principal, Roles: roles}, nil
}

// Authorize is Map followed by the no-escalation RBAC intersection: the
// returned roles are exactly those the policy mapped AND that the derived
// principal actually holds in NextSQL RBAC (held). An empty intersection is a
// Forbidden error — an external identity never yields a session without a real
// native grant.
func (p *IdentityPolicy) Authorize(issuer string, claims map[string]any, held []string) (MapResult, error) {
	r, err := p.Map(issuer, claims)
	if err != nil {
		return MapResult{}, err
	}
	r.Roles = IntersectRoles(r.Roles, held)
	if len(r.Roles) == 0 {
		return MapResult{}, nerr.New(nerr.Forbidden, "auth.IdentityPolicy",
			"principal holds none of the policy-mapped roles")
	}
	return r, nil
}

// IntersectRoles returns the members of mapped that also appear in held,
// normalized, deduped, and in mapped's order. This is the no-escalation gate:
// the result is what a broker would place in an NSSC1. credential's Roles list,
// which ACL.AllowedScoped already enforces.
func IntersectRoles(mapped, held []string) []string {
	h := make(map[string]struct{}, len(held))
	for _, r := range held {
		if r = normToken(r); r != "" {
			h[r] = struct{}{}
		}
	}
	out := make([]string, 0, len(mapped))
	seen := make(map[string]struct{}, len(mapped))
	for _, r := range mapped {
		r = normToken(r)
		if r == "" {
			continue
		}
		if _, ok := h[r]; !ok {
			continue
		}
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	return out
}

// --- evaluation helpers ---

func condsPass(conds []compiledCond, claims map[string]any) bool {
	for _, c := range conds {
		got, ok := claimString(claims, c.cond.Claim)
		if !ok {
			return false
		}
		switch c.cond.Op {
		case OpEquals:
			if got != c.cond.Value {
				return false
			}
		case OpHasPrefix:
			if !strings.HasPrefix(got, c.cond.Value) {
				return false
			}
		case OpHasSuffix:
			if !strings.HasSuffix(got, c.cond.Value) {
				return false
			}
		case OpRegex:
			if c.re == nil || !c.re.MatchString(got) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func derivePrincipal(t Principal, claims map[string]any) (string, error) {
	var s string
	switch t.Kind {
	case PrincipalLiteral:
		s = t.Value
	case PrincipalClaim:
		v, ok := claimString(claims, t.Value)
		if !ok {
			return "", nerr.New(nerr.Forbidden, "auth.IdentityPolicy", "principal claim is absent")
		}
		s = v
	default:
		return "", nerr.New(nerr.Forbidden, "auth.IdentityPolicy", "invalid principal template")
	}
	for _, tr := range t.Transforms {
		next, err := applyTransform(s, tr)
		if err != nil {
			return "", err
		}
		s = next
	}
	s = strings.ToLower(strings.TrimSpace(s))
	if !validLogin(s) {
		return "", nerr.New(nerr.Forbidden, "auth.IdentityPolicy", "derived principal is not a valid login name")
	}
	return s, nil
}

func applyTransform(s string, t Transform) (string, error) {
	bad := nerr.New(nerr.Forbidden, "auth.IdentityPolicy", "principal transform did not apply")
	switch t.Op {
	case TransformLower:
		return strings.ToLower(s), nil
	case TransformBefore:
		if t.A == "" {
			return "", bad
		}
		if i := strings.Index(s, t.A); i >= 0 {
			return s[:i], nil
		}
		return "", bad
	case TransformAfter:
		if t.A == "" {
			return "", bad
		}
		if i := strings.Index(s, t.A); i >= 0 {
			return s[i+len(t.A):], nil
		}
		return "", bad
	case TransformReplace:
		if t.A == "" {
			return "", bad
		}
		return strings.ReplaceAll(s, t.A, t.B), nil
	default:
		return "", bad
	}
}

func mapGroups(mappings []compiledMapping, groups []string) []string {
	var out []string
	seen := make(map[string]struct{})
	add := func(role string) {
		role = normToken(role)
		if role == "" {
			return
		}
		if _, ok := seen[role]; ok {
			return
		}
		seen[role] = struct{}{}
		out = append(out, role)
	}
	for _, cm := range mappings {
		for _, g := range groups {
			if cm.re != nil {
				m := cm.re.FindStringSubmatch(g)
				if m == nil {
					continue
				}
				for _, tmpl := range cm.m.Roles {
					role, err := expandRoleTemplate(tmpl, m)
					if err != nil {
						continue
					}
					add(role)
				}
				continue
			}
			if g == cm.m.Group {
				for _, role := range cm.m.Roles {
					add(role)
				}
			}
		}
	}
	return out
}

func expandRoleTemplate(tmpl string, sub []string) (string, error) {
	if !strings.Contains(tmpl, "${") {
		return tmpl, nil
	}
	var b strings.Builder
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] == '$' && i+1 < len(tmpl) && tmpl[i+1] == '{' {
			end := strings.IndexByte(tmpl[i+2:], '}')
			if end < 0 {
				return "", nerr.New(nerr.Forbidden, "auth.IdentityPolicy", "unterminated role template reference")
			}
			n, err := strconv.Atoi(tmpl[i+2 : i+2+end])
			if err != nil || n < 0 || n >= len(sub) {
				return "", nerr.New(nerr.Forbidden, "auth.IdentityPolicy", "role template reference out of range")
			}
			b.WriteString(sub[n])
			i += 2 + end
			continue
		}
		b.WriteByte(tmpl[i])
	}
	return b.String(), nil
}

// --- claim extraction ---

func claimLookup(claims map[string]any, path string) (any, bool) {
	if claims == nil || path == "" {
		return nil, false
	}
	var cur any = claims
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func claimString(claims map[string]any, path string) (string, bool) {
	v, ok := claimLookup(claims, path)
	if !ok {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	case json.Number:
		return t.String(), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	default:
		return "", false
	}
}

func claimStrings(claims map[string]any, path string) ([]string, bool) {
	v, ok := claimLookup(claims, path)
	if !ok {
		return nil, false
	}
	switch t := v.(type) {
	case string:
		return []string{t}, true
	case []string:
		return append([]string(nil), t...), true
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	default:
		return nil, false
	}
}

// --- validation / compilation ---

func validLogin(s string) bool {
	if s == "" || len(s) > maxPrincipalOut {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func validClaimName(s string) bool {
	if s == "" || len(s) > maxClaimNameLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == '-' || c == '/' || c == ':' {
			continue
		}
		return false
	}
	// no empty dotted segment
	if strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") || strings.Contains(s, "..") {
		return false
	}
	return true
}

func anchoredRegex(pattern string) (*regexp.Regexp, error) {
	if pattern == "" || len(pattern) > maxGroupPatternLen {
		return nil, nerr.New(nerr.InvalidArgument, "auth.IdentityPolicy", "invalid regex pattern length")
	}
	re, err := regexp.Compile("^(?:" + pattern + ")$")
	if err != nil {
		return nil, nerr.New(nerr.InvalidArgument, "auth.IdentityPolicy", "invalid RE2 pattern")
	}
	return re, nil
}

func compilePolicy(doc PolicyDoc) ([]compiledRule, []compiledMapping, error) {
	bad := func(msg string) ([]compiledRule, []compiledMapping, error) {
		return nil, nil, nerr.New(nerr.InvalidArgument, "auth.IdentityPolicy", msg)
	}
	if len(doc.SubjectRules) == 0 {
		return bad("policy has no subject rules")
	}
	if len(doc.SubjectRules) > maxSubjectRules {
		return bad("too many subject rules")
	}
	if len(doc.GroupMappings) > maxGroupMappings {
		return bad("too many group mappings")
	}
	if doc.GroupClaim != "" && !validClaimName(doc.GroupClaim) {
		return bad("invalid group claim name")
	}
	if len(doc.DefaultRoles) > maxDefaultRoles {
		return bad("too many default roles")
	}
	if _, err := normTokenRoles(doc.DefaultRoles); err != nil {
		return bad("invalid default role name")
	}

	rules := make([]compiledRule, 0, len(doc.SubjectRules))
	ids := make(map[string]struct{}, len(doc.SubjectRules))
	for _, r := range doc.SubjectRules {
		if r.ID == "" || len(r.ID) > maxRuleIDLen {
			return bad("invalid subject rule id")
		}
		if _, dup := ids[r.ID]; dup {
			return bad("duplicate subject rule id")
		}
		ids[r.ID] = struct{}{}
		if strings.TrimSpace(r.Issuer) == "" || len(r.Issuer) > maxIssuerLen {
			return bad("invalid subject rule issuer")
		}
		if len(r.Match) == 0 || len(r.Match) > maxRuleConds {
			return bad("subject rule needs 1..16 conditions")
		}
		cr := compiledRule{rule: r, conds: make([]compiledCond, 0, len(r.Match))}
		for _, c := range r.Match {
			if !validClaimName(c.Claim) {
				return bad("invalid condition claim name")
			}
			if !c.Op.valid() {
				return bad("invalid condition operator")
			}
			if c.Value == "" || len(c.Value) > maxMatchValueLen {
				return bad("invalid condition value")
			}
			cc := compiledCond{cond: c}
			if c.Op == OpRegex {
				re, err := anchoredRegex(c.Value)
				if err != nil {
					return nil, nil, err
				}
				cc.re = re
			}
			cr.conds = append(cr.conds, cc)
		}
		if !r.Principal.Kind.valid() {
			return bad("invalid principal kind")
		}
		if r.Principal.Value == "" || len(r.Principal.Value) > maxMatchValueLen {
			return bad("invalid principal source value")
		}
		if r.Principal.Kind == PrincipalClaim && !validClaimName(r.Principal.Value) {
			return bad("invalid principal claim name")
		}
		if len(r.Principal.Transforms) > maxRuleTransforms {
			return bad("too many principal transforms")
		}
		for _, tr := range r.Principal.Transforms {
			if !tr.Op.valid() {
				return bad("invalid principal transform op")
			}
			if len(tr.A) > maxMatchValueLen || len(tr.B) > maxMatchValueLen {
				return bad("principal transform argument too long")
			}
			if (tr.Op == TransformBefore || tr.Op == TransformAfter || tr.Op == TransformReplace) && tr.A == "" {
				return bad("principal transform requires a non-empty argument")
			}
		}
		rules = append(rules, cr)
	}

	mappings := make([]compiledMapping, 0, len(doc.GroupMappings))
	for _, m := range doc.GroupMappings {
		if m.Group == "" || len(m.Group) > maxGroupPatternLen {
			return bad("invalid group mapping key")
		}
		if len(m.Roles) == 0 || len(m.Roles) > maxMappingRoles {
			return bad("group mapping needs 1..16 roles")
		}
		cm := compiledMapping{m: m}
		if m.IsRegex {
			re, err := anchoredRegex(m.Group)
			if err != nil {
				return nil, nil, err
			}
			cm.re = re
			for _, role := range m.Roles {
				if role == "" || len(role) > maxTokenRoleLen {
					return bad("invalid group mapping role template")
				}
				if err := validateRoleTemplate(role, re.NumSubexp()); err != nil {
					return nil, nil, err
				}
			}
		} else {
			if _, err := normTokenRoles(m.Roles); err != nil {
				return bad("invalid group mapping role name")
			}
		}
		mappings = append(mappings, cm)
	}
	return rules, mappings, nil
}

func validateRoleTemplate(tmpl string, numSub int) error {
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] != '$' || i+1 >= len(tmpl) || tmpl[i+1] != '{' {
			continue
		}
		end := strings.IndexByte(tmpl[i+2:], '}')
		if end < 0 {
			return nerr.New(nerr.InvalidArgument, "auth.IdentityPolicy", "unterminated role template reference")
		}
		n, err := strconv.Atoi(tmpl[i+2 : i+2+end])
		if err != nil || n < 0 || n > numSub {
			return nerr.New(nerr.InvalidArgument, "auth.IdentityPolicy", "role template reference out of range")
		}
		i += 2 + end
	}
	return nil
}

// --- encode / decode ---

func encodeIdentityPolicy(doc PolicyDoc) ([]byte, error) {
	if _, _, err := compilePolicy(doc); err != nil {
		return nil, err
	}
	buf := make([]byte, 0, 256)
	buf = append(buf, idpMagic...)
	buf = appendU16(buf, idpVersion)
	buf = appendStr(buf, doc.GroupClaim)
	buf = appendStrSlice(buf, doc.DefaultRoles)

	buf = appendU16(buf, len(doc.SubjectRules))
	for _, r := range doc.SubjectRules {
		buf = appendStr(buf, r.ID)
		buf = appendStr(buf, r.Issuer)
		buf = appendU16(buf, len(r.Match))
		for _, c := range r.Match {
			buf = appendStr(buf, c.Claim)
			buf = append(buf, byte(c.Op))
			buf = appendStr(buf, c.Value)
		}
		buf = append(buf, byte(r.Principal.Kind))
		buf = appendStr(buf, r.Principal.Value)
		buf = append(buf, byte(len(r.Principal.Transforms)))
		for _, tr := range r.Principal.Transforms {
			buf = append(buf, byte(tr.Op))
			buf = appendStr(buf, tr.A)
			buf = appendStr(buf, tr.B)
		}
	}

	buf = appendU16(buf, len(doc.GroupMappings))
	for _, m := range doc.GroupMappings {
		buf = appendStr(buf, m.Group)
		if m.IsRegex {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
		buf = appendStrSlice(buf, m.Roles)
	}

	if len(buf) > maxIDPBlob {
		return nil, nerr.New(nerr.InvalidArgument, "auth.IdentityPolicy", "policy exceeds the size limit")
	}
	return buf, nil
}

func decodeIdentityPolicy(raw []byte) (PolicyDoc, error) {
	bad := func(msg string) (PolicyDoc, error) {
		return PolicyDoc{}, nerr.New(nerr.InvalidFormat, "auth.decodeIdentityPolicy", msg)
	}
	if len(raw) > maxIDPBlob {
		return bad("policy file too large")
	}
	if len(raw) < 8 {
		return bad("truncated policy")
	}
	if string(raw[0:4]) != idpMagic {
		return bad("bad policy magic")
	}
	if encoding.U16(raw, 4) != idpVersion {
		return bad("unsupported policy version")
	}
	d := &cursor{raw: raw, off: 6}

	var doc PolicyDoc
	var err error
	if doc.GroupClaim, err = d.str(maxClaimNameLen); err != nil {
		return bad("truncated group claim")
	}
	if doc.DefaultRoles, err = d.strSlice(maxDefaultRoles, maxTokenRoleLen); err != nil {
		return bad("truncated default roles")
	}

	nRules, err := d.u16()
	if err != nil || nRules > maxSubjectRules {
		return bad("bad subject rule count")
	}
	doc.SubjectRules = make([]SubjectRule, 0, nRules)
	for i := 0; i < nRules; i++ {
		var r SubjectRule
		if r.ID, err = d.str(maxRuleIDLen); err != nil {
			return bad("truncated rule id")
		}
		if r.Issuer, err = d.str(maxIssuerLen); err != nil {
			return bad("truncated rule issuer")
		}
		nConds, err := d.u16()
		if err != nil || nConds > maxRuleConds {
			return bad("bad rule condition count")
		}
		r.Match = make([]MatchCond, 0, nConds)
		for j := 0; j < nConds; j++ {
			var c MatchCond
			if c.Claim, err = d.str(maxClaimNameLen); err != nil {
				return bad("truncated condition claim")
			}
			op, err := d.u8()
			if err != nil {
				return bad("truncated condition op")
			}
			c.Op = MatchOp(op)
			if c.Value, err = d.str(maxMatchValueLen); err != nil {
				return bad("truncated condition value")
			}
			r.Match = append(r.Match, c)
		}
		kind, err := d.u8()
		if err != nil {
			return bad("truncated principal kind")
		}
		r.Principal.Kind = PrincipalKind(kind)
		if r.Principal.Value, err = d.str(maxMatchValueLen); err != nil {
			return bad("truncated principal value")
		}
		nTr, err := d.u8()
		if err != nil || int(nTr) > maxRuleTransforms {
			return bad("bad principal transform count")
		}
		r.Principal.Transforms = make([]Transform, 0, nTr)
		for j := 0; j < int(nTr); j++ {
			op, err := d.u8()
			if err != nil {
				return bad("truncated transform op")
			}
			tr := Transform{Op: TransformOp(op)}
			if tr.A, err = d.str(maxMatchValueLen); err != nil {
				return bad("truncated transform arg A")
			}
			if tr.B, err = d.str(maxMatchValueLen); err != nil {
				return bad("truncated transform arg B")
			}
			r.Principal.Transforms = append(r.Principal.Transforms, tr)
		}
		doc.SubjectRules = append(doc.SubjectRules, r)
	}

	nMap, err := d.u16()
	if err != nil || nMap > maxGroupMappings {
		return bad("bad group mapping count")
	}
	doc.GroupMappings = make([]GroupMapping, 0, nMap)
	for i := 0; i < nMap; i++ {
		var m GroupMapping
		if m.Group, err = d.str(maxGroupPatternLen); err != nil {
			return bad("truncated group mapping key")
		}
		flag, err := d.u8()
		if err != nil || flag > 1 {
			return bad("bad group mapping flag")
		}
		m.IsRegex = flag == 1
		if m.Roles, err = d.strSlice(maxMappingRoles, maxTokenRoleLen); err != nil {
			return bad("truncated group mapping roles")
		}
		doc.GroupMappings = append(doc.GroupMappings, m)
	}

	if !d.done() {
		return bad("trailing policy bytes")
	}
	if _, _, err := compilePolicy(doc); err != nil {
		return PolicyDoc{}, err
	}
	return doc, nil
}

// cursor is a bounds-checked reader over a policy blob.
type cursor struct {
	raw []byte
	off int
}

func (d *cursor) done() bool { return d.off == len(d.raw) }

func (d *cursor) u8() (byte, error) {
	if d.off+1 > len(d.raw) {
		return 0, nerr.New(nerr.InvalidFormat, "auth.decodeIdentityPolicy", "truncated byte")
	}
	v := d.raw[d.off]
	d.off++
	return v, nil
}

func (d *cursor) u16() (int, error) {
	v, err := encoding.ReadU16(d.raw, d.off)
	if err != nil {
		return 0, err
	}
	d.off += 2
	return int(v), nil
}

func (d *cursor) str(max int) (string, error) {
	n, err := encoding.ReadU16(d.raw, d.off)
	if err != nil || int(n) > max {
		return "", nerr.New(nerr.InvalidFormat, "auth.decodeIdentityPolicy", "bad string length")
	}
	b, err := encoding.ReadBytes(d.raw, d.off+2, int(n))
	if err != nil {
		return "", err
	}
	d.off += 2 + int(n)
	return string(b), nil
}

func (d *cursor) strSlice(maxCount, maxLen int) ([]string, error) {
	n, err := d.u16()
	if err != nil || n > maxCount {
		return nil, nerr.New(nerr.InvalidFormat, "auth.decodeIdentityPolicy", "bad string slice count")
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		s, err := d.str(maxLen)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func appendU16(dst []byte, v int) []byte {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], uint16(v))
	return append(dst, b[:]...)
}

func appendStr(dst []byte, s string) []byte {
	dst = appendU16(dst, len(s))
	return append(dst, s...)
}

func appendStrSlice(dst []byte, ss []string) []byte {
	dst = appendU16(dst, len(ss))
	for _, s := range ss {
		dst = appendStr(dst, s)
	}
	return dst
}

func cloneDoc(in PolicyDoc) PolicyDoc {
	out := PolicyDoc{
		GroupClaim:   in.GroupClaim,
		DefaultRoles: append([]string(nil), in.DefaultRoles...),
	}
	for _, r := range in.SubjectRules {
		cr := SubjectRule{
			ID:     r.ID,
			Issuer: r.Issuer,
			Match:  append([]MatchCond(nil), r.Match...),
			Principal: Principal{
				Kind:       r.Principal.Kind,
				Value:      r.Principal.Value,
				Transforms: append([]Transform(nil), r.Principal.Transforms...),
			},
		}
		out.SubjectRules = append(out.SubjectRules, cr)
	}
	for _, m := range in.GroupMappings {
		out.GroupMappings = append(out.GroupMappings, GroupMapping{
			Group:   m.Group,
			IsRegex: m.IsRegex,
			Roles:   append([]string(nil), m.Roles...),
		})
	}
	return out
}
