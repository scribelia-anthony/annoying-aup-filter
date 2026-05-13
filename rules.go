package main

import (
	"bytes"
	"fmt"
	"regexp"
	"sync"
)

type RulePhase string

const (
	PhaseRequest  RulePhase = "request"
	PhaseResponse RulePhase = "response"
)

type RuleTarget string

const (
	TargetURL    RuleTarget = "url"
	TargetHeader RuleTarget = "header"
	TargetBody   RuleTarget = "body"
)

type Rule struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Enabled     bool       `json:"enabled"`
	Phase       RulePhase  `json:"phase"`
	Target      RuleTarget `json:"target"`
	HeaderName  string     `json:"header_name,omitempty"`
	Match       string     `json:"match"`
	Replacement string     `json:"replacement"`

	re *regexp.Regexp
}

func (r *Rule) compile() error {
	if r.Match == "" {
		return fmt.Errorf("rule %q: empty match", r.Name)
	}
	re, err := regexp.Compile(r.Match)
	if err != nil {
		return fmt.Errorf("rule %q: %w", r.Name, err)
	}
	r.re = re
	return nil
}

type Rules struct {
	mu    sync.RWMutex
	rules []*Rule
}

func NewRules() *Rules {
	return &Rules{}
}

func (rs *Rules) List() []*Rule {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	out := make([]*Rule, len(rs.rules))
	copy(out, rs.rules)
	return out
}

func (rs *Rules) Replace(rules []*Rule) error {
	compiled := make([]*Rule, 0, len(rules))
	for _, r := range rules {
		if err := r.compile(); err != nil {
			return err
		}
		if r.ID == "" {
			r.ID = newID()
		}
		compiled = append(compiled, r)
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.rules = compiled
	return nil
}

// ApplyRequest rewrites the URL/headers/body in place using request-phase rules.
// Returns true if anything was modified.
func (rs *Rules) ApplyRequest(urlStr *string, headers map[string][]string, body *[]byte) bool {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	modified := false
	for _, r := range rs.rules {
		if !r.Enabled || r.Phase != PhaseRequest {
			continue
		}
		switch r.Target {
		case TargetURL:
			n := r.re.ReplaceAllString(*urlStr, r.Replacement)
			if n != *urlStr {
				*urlStr = n
				modified = true
			}
		case TargetHeader:
			if r.HeaderName == "" {
				continue
			}
			vals, ok := headers[r.HeaderName]
			if !ok {
				continue
			}
			for i, v := range vals {
				n := r.re.ReplaceAllString(v, r.Replacement)
				if n != v {
					vals[i] = n
					modified = true
				}
			}
			headers[r.HeaderName] = vals
		case TargetBody:
			if len(*body) == 0 {
				continue
			}
			n := r.re.ReplaceAll(*body, []byte(r.Replacement))
			if !bytes.Equal(n, *body) {
				*body = n
				modified = true
			}
		}
	}
	return modified
}

func (rs *Rules) ApplyResponseHeaders(headers map[string][]string) bool {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	modified := false
	for _, r := range rs.rules {
		if !r.Enabled || r.Phase != PhaseResponse || r.Target != TargetHeader {
			continue
		}
		if r.HeaderName == "" {
			continue
		}
		vals, ok := headers[r.HeaderName]
		if !ok {
			continue
		}
		for i, v := range vals {
			n := r.re.ReplaceAllString(v, r.Replacement)
			if n != v {
				vals[i] = n
				modified = true
			}
		}
		headers[r.HeaderName] = vals
	}
	return modified
}

// ApplyResponseBody rewrites a chunk of response body in place.
// NOTE: matches that span chunk boundaries are missed in streaming responses.
func (rs *Rules) ApplyResponseBody(body *[]byte) bool {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	modified := false
	for _, r := range rs.rules {
		if !r.Enabled || r.Phase != PhaseResponse || r.Target != TargetBody {
			continue
		}
		if len(*body) == 0 {
			continue
		}
		n := r.re.ReplaceAll(*body, []byte(r.Replacement))
		if !bytes.Equal(n, *body) {
			*body = n
			modified = true
		}
	}
	return modified
}
