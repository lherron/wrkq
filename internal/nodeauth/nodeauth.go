// Package nodeauth resolves wrkqd bearer tokens to logical node identities.
//
// A shared bearer token cannot distinguish one logical node from another
// (co-hosted nodes share a machine, so peer IP is not an identity either).
// wrkqd therefore maps each bearer token to exactly one nodeId server-side;
// callers never supply their own node identity.
//
// Token values are held only as SHA-256 digests and never appear in error
// text or logs.
package nodeauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
)

// ReservedNodeID is forbidden as a nodeId: "local" means "wherever the caller
// is", which is the opposite of an identity.
const ReservedNodeID = "local"

const maxNodeIDLen = 64

// Registry maps bearer tokens to nodeIds. The zero value is a disabled
// registry that resolves nothing.
type Registry struct {
	byTokenHash map[string]string
}

// Enabled reports whether per-node token auth is configured.
func (r *Registry) Enabled() bool {
	return r != nil && len(r.byTokenHash) > 0
}

// Resolve returns the nodeId the token authenticates as. A token that is
// empty or unmapped resolves to nothing, which callers must treat as an
// auth refusal.
func (r *Registry) Resolve(token string) (string, bool) {
	if !r.Enabled() || token == "" {
		return "", false
	}
	node, ok := r.byTokenHash[hashToken(token)]
	return node, ok
}

// Nodes lists the configured nodeIds in sorted order. It exposes no token
// material and is safe to log.
func (r *Registry) Nodes() []string {
	if !r.Enabled() {
		return nil
	}
	seen := make(map[string]struct{}, len(r.byTokenHash))
	nodes := make([]string, 0, len(r.byTokenHash))
	for _, node := range r.byTokenHash {
		if _, dup := seen[node]; dup {
			continue
		}
		seen[node] = struct{}{}
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	return nodes
}

// ParseSpec builds a Registry from `nodeId=token` entries separated by commas
// or newlines. Blank entries and `#` comment lines are ignored.
//
// A token mapped to more than one node is a config error, as is an invalid or
// reserved nodeId. A node may hold several tokens (rotation), but the same
// token value may not be listed twice.
func ParseSpec(spec string) (*Registry, error) {
	reg := &Registry{byTokenHash: map[string]string{}}
	entry := 0
	for _, raw := range strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || r == '\n' }) {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entry++
		node, token, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("node token entry %d: expected nodeId=token", entry)
		}
		node = strings.TrimSpace(node)
		token = strings.TrimSpace(token)
		if err := ValidateNodeID(node); err != nil {
			return nil, fmt.Errorf("node token entry %d: %w", entry, err)
		}
		if token == "" {
			return nil, fmt.Errorf("node token entry %d (node %q): empty token", entry, node)
		}
		hash := hashToken(token)
		if existing, dup := reg.byTokenHash[hash]; dup {
			return nil, fmt.Errorf("node token entry %d (node %q): token is already mapped to node %q", entry, node, existing)
		}
		reg.byTokenHash[hash] = node
	}
	if len(reg.byTokenHash) == 0 {
		return nil, fmt.Errorf("no node tokens configured")
	}
	return reg, nil
}

// LoadFile reads a node token file written in the ParseSpec grammar.
func LoadFile(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read node token file: %w", err)
	}
	reg, err := ParseSpec(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return reg, nil
}

// ValidateNodeID enforces the federation nodeId grammar: [A-Za-z0-9._-],
// 1-64 characters, and never the reserved literal "local".
func ValidateNodeID(id string) error {
	if id == "" {
		return fmt.Errorf("empty nodeId")
	}
	if len(id) > maxNodeIDLen {
		return fmt.Errorf("nodeId %q exceeds %d characters", id, maxNodeIDLen)
	}
	if id == ReservedNodeID {
		return fmt.Errorf("nodeId %q is reserved", ReservedNodeID)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return fmt.Errorf("nodeId %q contains invalid character %q", id, r)
		}
	}
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type ctxKey struct{}

// WithNode annotates a request context with the authenticated nodeId.
func WithNode(ctx context.Context, nodeID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, nodeID)
}

// FromContext returns the nodeId the request authenticated as, if per-node
// token auth resolved one.
func FromContext(ctx context.Context) (string, bool) {
	node, ok := ctx.Value(ctxKey{}).(string)
	return node, ok && node != ""
}
