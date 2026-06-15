package mock

import (
	"context"
	"strconv"
	"strings"

	"github.com/ngoldack/fin-mcp/internal/config"
	"github.com/ngoldack/fin-mcp/internal/setupflow"
)

func init() { setupflow.Register(&Flow{}) }

// Flow implements setupflow.Flow for the in-memory mock provider. It needs no
// credentials and no bank authorization; connections are nominal.
type Flow struct{}

func (*Flow) Type() config.ProviderType { return config.ProviderMock }

func (*Flow) NeedsAuthorization() bool { return false }

func (*Flow) CredentialFields() []setupflow.Field {
	return []setupflow.Field{
		{Key: "accounts", Label: "Number of seeded accounts", Kind: setupflow.FieldText, Default: "1", Optional: true},
	}
}

func (*Flow) ApplyCredentials(pc *config.ProviderConfig, values map[string]string) (string, error) {
	if pc.Mock == nil {
		pc.Mock = &config.MockProviderConfig{}
	}
	if n, err := strconv.Atoi(strings.TrimSpace(values["accounts"])); err == nil && n > 0 {
		pc.Mock.Accounts = n
	}
	return "", nil
}

func (*Flow) Banks(context.Context, *config.ProviderConfig, string) ([]setupflow.Bank, error) {
	return nil, nil
}

func (*Flow) StartConnection(context.Context, *config.ProviderConfig, setupflow.ConnectionRequest) (string, error) {
	return "", nil
}

func (*Flow) CompleteConnection(_ context.Context, _ *config.ProviderConfig, req setupflow.ConnectionRequest) (config.Connection, error) {
	name := req.Name
	if name == "" {
		name = "mock-conn"
	}
	return config.Connection{Name: name, Bank: "Mock Bank", Country: "DE"}, nil
}
