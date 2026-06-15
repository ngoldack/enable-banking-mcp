// Package setupflow defines the provider-agnostic setup port. Each provider
// type implements its own credential bootstrap and connection-authorization
// flow; the CLI and the TUI wizard drive any provider through this single
// interface.
//
// It is a leaf package (depends only on config) so provider adapter packages
// can implement it without creating an import cycle. Adapters self-register
// their flow from an init() function.
package setupflow

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ngoldack/fin-mcp/internal/config"
)

// FieldKind hints how a credential field should be collected and rendered.
type FieldKind string

const (
	FieldText   FieldKind = "text"
	FieldSecret FieldKind = "secret"
	FieldChoice FieldKind = "choice"
)

// Field describes one provider-credential input the setup UI should collect.
type Field struct {
	Key      string
	Label    string
	Kind     FieldKind
	Default  string
	Choices  []string // for FieldChoice
	Optional bool
}

// Bank is a selectable institution (ASPSP) for providers that expose a directory.
type Bank struct {
	Name    string
	Country string
	BIC     string
}

// ConnectionRequest carries the inputs for starting and completing a connection.
type ConnectionRequest struct {
	Bank Bank
	Name string // desired connection name ("" => the flow derives a slug)
	Code string // authorization code (Complete step, when NeedsAuthorization)
	Days int    // consent validity in days
}

// Flow drives one provider type's setup: credential bootstrap plus connection
// authorization. Implementations live in the provider adapter packages.
type Flow interface {
	Type() config.ProviderType

	// CredentialFields lists the provider-credential inputs needed to configure a
	// provider instance (nil if none, e.g. the mock provider).
	CredentialFields() []Field

	// ApplyCredentials writes collected field values into pc, performing any side
	// effects (e.g. generating an RSA key pair). It returns optional human
	// instructions to display (e.g. "upload public.crt to the dashboard").
	ApplyCredentials(pc *config.ProviderConfig, values map[string]string) (instructions string, err error)

	// NeedsAuthorization reports whether adding a connection requires the
	// bank-redirect (SCA) flow. When false, CompleteConnection is a single step.
	NeedsAuthorization() bool

	// Banks lists selectable institutions for a country (nil if not applicable).
	Banks(ctx context.Context, pc *config.ProviderConfig, country string) ([]Bank, error)

	// StartConnection begins authorizing a connection and returns an SCA redirect
	// URL (empty when NeedsAuthorization is false).
	StartConnection(ctx context.Context, pc *config.ProviderConfig, req ConnectionRequest) (authURL string, err error)

	// CompleteConnection finalizes a connection (exchanging the code when
	// applicable) and returns the connection to persist.
	CompleteConnection(ctx context.Context, pc *config.ProviderConfig, req ConnectionRequest) (config.Connection, error)
}

var registry = map[config.ProviderType]Flow{}

// Register adds a flow. Adapter packages call this from init().
func Register(f Flow) { registry[f.Type()] = f }

// For returns the flow registered for a provider type.
func For(t config.ProviderType) (Flow, bool) {
	f, ok := registry[t]
	return f, ok
}

// MustFor returns the flow for a provider type, or an error if none is registered.
func MustFor(t config.ProviderType) (Flow, error) {
	if f, ok := registry[t]; ok {
		return f, nil
	}
	return nil, fmt.Errorf("no setup flow registered for provider type %q", t)
}

// Types returns the registered provider types in sorted order.
func Types() []config.ProviderType {
	out := make([]config.ProviderType, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Upsert replaces a connection with the same name on pc, or appends it.
func Upsert(pc *config.ProviderConfig, conn config.Connection) {
	for i := range pc.Connections {
		if pc.Connections[i].Name == conn.Name {
			pc.Connections[i] = conn
			return
		}
	}
	pc.Connections = append(pc.Connections, conn)
}

// Slug normalizes a label into a connection name (lowercase, dashed).
func Slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	if s == "" {
		s = "connection"
	}
	return s
}
