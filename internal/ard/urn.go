// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"fmt"
	"regexp"
	"strings"
)

// URN namespace identifier for AI Resources.
const URNNamespace = "air"

// URNPrefix is the standard ARD URN prefix.
const URNPrefix = "urn:air:"

// URN regex pattern: urn:air:<publisher>:<namespace>:<name>
// Publisher is a domain, namespace and name are alphanumeric with dashes/underscores
var urnPattern = regexp.MustCompile(`^urn:air:([a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}(:[a-zA-Z0-9\-_]+)?(:[a-zA-Z0-9\-_]+)$`)

// URN represents an ARD URN identifier.
type URN struct {
	Publisher  string   // Fully qualified domain name
	Namespace  string   // Optional hierarchical namespace
	Name       string   // Resource name
	Raw        string   // Original URN string
}

// ParseURN parses a URN string into a URN struct.
func ParseURN(urnStr string) (*URN, error) {
	if !strings.HasPrefix(urnStr, URNPrefix) {
		return nil, fmt.Errorf("invalid URN prefix: must start with %s", URNPrefix)
	}

	parts := strings.Split(urnStr[8:], ":") // Remove "urn:air:" prefix
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid URN format: expected urn:air:<publisher>:<name>")
	}

	// First part is publisher (domain), rest is namespace:name
	publisher := parts[0]
	
	// Validate publisher is a valid domain
	if !isValidDomain(publisher) {
		return nil, fmt.Errorf("invalid publisher domain: %s", publisher)
	}

	var namespace, name string
	if len(parts) >= 3 {
		namespace = parts[1]
		name = strings.Join(parts[2:], ":") // Name can contain colons
	} else {
		name = parts[1]
	}

	if name == "" {
		return nil, fmt.Errorf("URN name cannot be empty")
	}

	return &URN{
		Publisher: publisher,
		Namespace: namespace,
		Name:      name,
		Raw:       urnStr,
	}, nil
}

// ValidateURN validates a URN string format.
func ValidateURN(urnStr string) error {
	_, err := ParseURN(urnStr)
	return err
}

// NewURN creates a new URN with the given components.
func NewURN(publisher, namespace, name string) *URN {
	var parts []string
	if namespace != "" {
		parts = []string{publisher, namespace, name}
	} else {
		parts = []string{publisher, name}
	}
	
	raw := URNPrefix + strings.Join(parts, ":")
	return &URN{
		Publisher: publisher,
		Namespace: namespace,
		Name:      name,
		Raw:       raw,
	}
}

// String returns the URN as a string.
func (u *URN) String() string {
	return u.Raw
}

// FullNamespace returns the full namespace including publisher.
func (u *URN) FullNamespace() string {
	if u.Namespace != "" {
		return fmt.Sprintf("%s:%s", u.Publisher, u.Namespace)
	}
	return u.Publisher
}

// MatchesFilter checks if the URN matches a publisher/namespace filter.
func (u *URN) MatchesFilter(publisher, namespace string) bool {
	if publisher != "" && u.Publisher != publisher {
		return false
	}
	if namespace != "" && u.Namespace != namespace {
		return false
	}
	return true
}

// URNBuilder helps build valid URNs.
type URNBuilder struct {
	publisher  string
	namespaces []string
}

// NewURNBuilder creates a new URN builder.
func NewURNBuilder(publisher string) (*URNBuilder, error) {
	if !isValidDomain(publisher) {
		return nil, fmt.Errorf("invalid publisher domain: %s", publisher)
	}
	return &URNBuilder{
		publisher:  publisher,
		namespaces: []string{},
	}, nil
}

// WithNamespace adds a namespace level.
func (b *URNBuilder) WithNamespace(namespace string) *URNBuilder {
	b.namespaces = append(b.namespaces, namespace)
	return b
}

// Build creates a URN with the given resource name.
func (b *URNBuilder) Build(name string) *URN {
	ns := ""
	if len(b.namespaces) > 0 {
		ns = strings.Join(b.namespaces, ":")
	}
	return NewURN(b.publisher, ns, name)
}

// BuildString creates a URN string with the given resource name.
func (b *URNBuilder) BuildString(name string) string {
	return b.Build(name).String()
}

// Predefined URN builders for common publishers.
var (
	// WukongLocal is the URN builder for local wukong installation.
	WukongLocal, _ = NewURNBuilder("wukong.local")
	
	// WukongOrg is the URN builder for wukong organization.
	WukongOrg, _ = NewURNBuilder("wukong.ai")
)

// Common Wukong resource URNs.
var WukongURNs = struct {
	// Server URNs
	AppsServer           *URN
	DeveloperServer     *URN
	BrowserServer      *URN
	MemoryServer        *URN
	ComputerServer      *URN
	
	// Agent URNs
	CortexAgent         *URN
	EvolutionAgent      *URN
	RecipeAgent         *URN
	
	// Tool URNs
	DeveloperTools      *URN
	BrowserTools        *URN
	MemoryTools         *URN
	APITools            *URN
}{
	AppsServer:        WukongLocal.Build("server:apps"),
	DeveloperServer:   WukongLocal.Build("server:developer"),
	BrowserServer:    WukongLocal.Build("server:browser"),
	MemoryServer:     WukongLocal.Build("server:memory"),
	ComputerServer:   WukongLocal.Build("server:computer"),
	
	CortexAgent:      WukongLocal.Build("agent:cortex"),
	EvolutionAgent:   WukongLocal.Build("agent:evolution"),
	RecipeAgent:      WukongLocal.Build("agent:recipe"),
	
	DeveloperTools:    WukongLocal.Build("tools:developer"),
	BrowserTools:     WukongLocal.Build("tools:browser"),
	MemoryTools:      WukongLocal.Build("tools:memory"),
	APITools:         WukongLocal.Build("tools:api"),
}

// isValidDomain checks if a string is a valid domain name.
func isValidDomain(domain string) bool {
	if domain == "" {
		return false
	}
	
	// Basic domain validation
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return false
	}
	
	for _, part := range parts {
		if len(part) == 0 || len(part) > 63 {
			return false
		}
		// Each part must start/end with alphanumeric
		for i, c := range part {
			if !isDomainChar(c) {
				return false
			}
			if (i == 0 || i == len(part)-1) && !isAlphanumeric(c) {
				return false
			}
		}
	}
	
	return true
}

func isDomainChar(c rune) bool {
	return isAlphanumeric(c) || c == '-' || c == '_'
}

func isAlphanumeric(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
