// Package policy answers permission requests from a declarative rule file, so
// a routine ask can be decided without a person and an unattended chain does
// not stall on it. Anything a rule does not match stays a question.
//
// The format is deliberately small: an ordered list of rules, first match
// wins, and a request nothing matches keeps the inbox's behavior. A policy
// that silently approves is worse than one that was rejected, so a rule that
// constrains nothing is refused rather than allowed to match everything.
package policy

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Decision is what a rule says to do with a matching request.
type Decision string

const (
	Allow Decision = "allow"
	Deny  Decision = "deny"
	Ask   Decision = "ask"
)

func (d Decision) valid() bool {
	switch d {
	case Allow, Deny, Ask:
		return true
	default:
		return false
	}
}

// Rule matches a permission request. An empty field matches anything; a
// pattern may use * for any run of characters and ? for one.
type Rule struct {
	Agent    string   `json:"agent,omitempty"`
	Tool     string   `json:"tool,omitempty"`
	Match    string   `json:"match,omitempty"`
	Decision Decision `json:"decision"`
}

// Policy is an ordered rule list.
type Policy struct {
	Rules []Rule `json:"rules"`
}

// Parse reads and validates a policy. It rejects anything that would only
// fail when a permission arrives, because at that point the fallback is a
// question the caller may never see.
func Parse(data []byte) (Policy, error) {
	var parsed Policy
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return Policy{}, fmt.Errorf("parse policy: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Policy{}, fmt.Errorf("parse policy: unexpected trailing data")
	}
	if err := parsed.validate(); err != nil {
		return Policy{}, err
	}
	return parsed, nil
}

func (p Policy) validate() error {
	for index, rule := range p.Rules {
		if !rule.Decision.valid() {
			return fmt.Errorf("rule %d has decision %q; use allow, deny or ask", index+1, rule.Decision)
		}
		if rule.Agent == "" && rule.Tool == "" && rule.Match == "" && rule.Decision != Ask {
			return fmt.Errorf("rule %d would match every request with %q; add an agent, tool or match", index+1, rule.Decision)
		}
	}
	return nil
}

// LoadFile reads a policy from disk. A missing file is not an error: it means
// the file configures nothing, which is the default.
func LoadFile(path string) (Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Policy{}, nil
		}
		return Policy{}, fmt.Errorf("read policy %s: %w", path, err)
	}
	return Parse(data)
}

// Decide returns the decision of the first matching rule and a description of
// it, or Ask and an empty description when nothing matches.
func (p Policy) Decide(agentName, tool, target string) (Decision, string) {
	for index, rule := range p.Rules {
		if !matches(rule.Agent, agentName) || !matches(rule.Tool, tool) || !matches(rule.Match, target) {
			continue
		}
		return rule.Decision, Describe(index, rule)
	}
	return Ask, ""
}

// Describe names a rule the way an audit entry and agent.explain should: what
// it matched and what it decided.
func Describe(index int, rule Rule) string {
	parts := []string{fmt.Sprintf("rules[%d]", index+1)}
	if rule.Agent != "" {
		parts = append(parts, "agent="+rule.Agent)
	}
	if rule.Tool != "" {
		parts = append(parts, "tool="+rule.Tool)
	}
	if rule.Match != "" {
		parts = append(parts, "match="+rule.Match)
	}
	parts = append(parts, string(rule.Decision))
	return strings.Join(parts, " ")
}

// matches applies one pattern to one value. An empty pattern matches
// everything; * and ? behave the way they do in a shell.
func matches(pattern, value string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	p := []rune(pattern)
	v := []rune(value)
	patternIndex, valueIndex := 0, 0
	starIndex, starValue := -1, 0
	for valueIndex < len(v) {
		switch {
		case patternIndex < len(p) && (p[patternIndex] == '?' || p[patternIndex] == v[valueIndex]):
			patternIndex++
			valueIndex++
		case patternIndex < len(p) && p[patternIndex] == '*':
			starIndex = patternIndex
			starValue = valueIndex
			patternIndex++
		case starIndex >= 0:
			patternIndex = starIndex + 1
			starValue++
			valueIndex = starValue
		default:
			return false
		}
	}
	for patternIndex < len(p) && p[patternIndex] == '*' {
		patternIndex++
	}
	return patternIndex == len(p)
}
