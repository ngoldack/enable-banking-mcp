package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ngoldack/fin-mcp/internal/bank"
	"github.com/ngoldack/fin-mcp/internal/config"
	"github.com/ngoldack/fin-mcp/internal/provider/mock"
)

func testModel(t *testing.T) *Model {
	t.Helper()
	cfg := &config.Config{
		EnableBanking: config.EnableBankingConfig{
			Environment:       "SANDBOX",
			BankName:          "Mock ASPSP",
			BankCountry:       "DE",
			SessionID:         "session-abcdef123456",
			ConsentValidUntil: time.Now().Add(72 * time.Hour),
		},
		MCP: config.MCPConfig{
			AccessMode:      config.ReadOnly,
			Transport:       config.TransportStdio,
			CacheTTLMinutes: 5,
			LogFormat:       config.LogFormatText,
			LogLevel:        "info",
		},
	}
	cache := bank.NewCache(t.TempDir()+"/cache.db", 5*time.Minute)
	t.Cleanup(func() { _ = cache.Close() })
	return newModel("config.json", cfg, mock.New(), cache)
}

func update(t *testing.T, m *Model, msg tea.Msg) (*Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	mm, ok := updated.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *Model", updated)
	}
	return mm, cmd
}

func TestDashboard_OverviewAndDetailFlow(t *testing.T) {
	m := testModel(t)

	m, _ = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	accounts := []bank.Account{{ID: "acc-1", Name: "Main Checking", Currency: "EUR", IBAN: "DE89370400440532013000"}}
	m, _ = update(t, m, accountsMsg(accounts))
	if m.state != viewOverview {
		t.Fatalf("state = %v, want viewOverview", m.state)
	}
	view := m.View()
	for _, want := range []string{"Operator Console", "Accounts (1)", "Main Checking", "SANDBOX", "ReadOnly", "valid for"} {
		if !strings.Contains(view, want) {
			t.Errorf("overview view missing %q\n---\n%s", want, view)
		}
	}

	m, cmd := update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != viewLoading {
		t.Fatalf("after enter, state = %v, want viewLoading", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a command after enter")
	}

	m, _ = update(t, m, accountDetailMsg{
		balances:     []bank.AccountBalance{{Name: "Booked Balance", Amount: "120.45"}},
		transactions: []bank.Transaction{{Date: "2026-06-14", Description: "To: Amazon", Amount: "-15.50", Currency: "EUR", Status: "Completed"}},
	})
	if m.state != viewDetail {
		t.Fatalf("state = %v, want viewDetail", m.state)
	}
	detail := m.View()
	for _, want := range []string{"Booked Balance", "Amazon", "Balances", "Recent Transactions"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail view missing %q\n---\n%s", want, detail)
		}
	}

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	if !m.showAbbrev || !strings.Contains(m.View(), "abbreviations") {
		t.Errorf("expected abbreviations guide after pressing h")
	}
}

func TestDashboard_MCPConfigOverlay(t *testing.T) {
	m := testModel(t)
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = update(t, m, accountsMsg([]bank.Account{{ID: "acc-1", Name: "Main", Currency: "EUR"}}))

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if m.state != viewConfig {
		t.Fatalf("state = %v, want viewConfig", m.state)
	}
	cfgView := m.View()
	for _, want := range []string{"mcpServers", "fin-mcp", "server", "--config"} {
		if !strings.Contains(cfgView, want) {
			t.Errorf("config view missing %q\n---\n%s", want, cfgView)
		}
	}

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.state != viewOverview {
		t.Errorf("after esc, state = %v, want viewOverview", m.state)
	}
}

func TestDashboard_QuitAndErrorView(t *testing.T) {
	m := testModel(t)
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m, _ = update(t, m, errorMsg(context.DeadlineExceeded))
	if m.state != viewError {
		t.Fatalf("state = %v, want viewError", m.state)
	}
	if v := m.View(); !strings.Contains(v, "Error") && !strings.Contains(v, "❌") {
		t.Errorf("error view should show an error indicator:\n%s", v)
	}

	_, cmd := update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q should produce tea.QuitMsg")
	}
}

func TestHelpers(t *testing.T) {
	if got := humanizeDuration(73 * time.Hour); got != "3d 1h" {
		t.Errorf("humanizeDuration(73h) = %q, want %q", got, "3d 1h")
	}
	if got := humanizeDuration(90 * time.Minute); got != "1h 30m" {
		t.Errorf("humanizeDuration(90m) = %q, want %q", got, "1h 30m")
	}
	if got := shorten("abcdefghij", 4); got != "abcd…" {
		t.Errorf("shorten = %q, want %q", got, "abcd…")
	}
	if got := truncate("hello world", 8); got != "hello w…" {
		t.Errorf("truncate = %q, want %q", got, "hello w…")
	}

	expired := &config.Config{}
	if _, style := consentStatus(expired); style.GetForeground() != tipStyle.GetForeground() {
		t.Errorf("zero consent should map to the unknown/tip style")
	}
}
