package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairaph/mcp-wizard/cli"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"github.com/sairaph/creality-slicer-mcp/internal/guide"
)

func TestMCPServerRefusesRemote(t *testing.T) {
	if code := runMCPServer(context.Background(), cli.Command{Name: "mcp", Remote: "https://example.invalid/mcp"}); code != 2 {
		t.Fatalf("exit code %d, want 2: this server never bridges", code)
	}
}

func TestMCPServerExitsTwoOnABadSetting(t *testing.T) {
	// The server must stop before it serves anything, so no stdio session starts.
	t.Setenv(domain.EnvCmd, filepath.Join(t.TempDir(), "missing", "CrealityPrint.exe"))
	if code := runMCPServer(context.Background(), cli.Command{Name: "mcp"}); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
}

func TestUninstallAllRefusals(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		cmd  cli.Command
	}{
		{"--all with --clients", cli.Command{All: true, Clients: []string{"cursor"}, DryRun: true}},
		{"--all with --scope project", cli.Command{All: true, Scope: "project", Dir: t.TempDir(), DryRun: true}},
	}
	for _, c := range cases {
		if code := runUninstall(ctx, c.cmd); code != 2 {
			t.Errorf("%s: exit code %d, want 2", c.name, code)
		}
	}
}

func TestIsClientHangup(t *testing.T) {
	if !isClientHangup(errors.New("server is closing: EOF")) || !isClientHangup(io.EOF) {
		t.Error("an EOF was not taken for a hangup")
	}
	if isClientHangup(errors.New("listen tcp: address already in use")) {
		t.Error("another error was taken for a hangup")
	}
}

func TestOneShotCommandsAreRegistered(t *testing.T) {
	if ok, _ := oneShotCommands.Dispatch(context.Background(), "no-such-command", nil); ok {
		t.Error("an unknown command was dispatched")
	}
	// A command with a bad flag is dispatched and fails with a usage exit code,
	// without ever reaching the tools.
	for _, name := range []string{"status", "presets"} {
		if ok, code := oneShotCommands.Dispatch(context.Background(), name, []string{"--no-such-flag"}); !ok || code != 2 {
			t.Errorf("%s: dispatched=%v code=%d, want a usage error (2)", name, ok, code)
		}
	}
	var usage bytes.Buffer
	oneShotCommands.PrintUsage(&usage)
	for _, want := range []string{"status", "presets"} {
		if !strings.Contains(usage.String(), want) {
			t.Errorf("usage lacks %q:\n%s", want, usage.String())
		}
	}
}

func TestAppMenuOffersDoctorAndStatus(t *testing.T) {
	var labels []string
	for _, it := range menuItems() {
		labels = append(labels, it.Label)
	}
	if got := strings.Join(labels, ","); got != "Run doctor,Show slicer status,Recent projects,Quit" {
		t.Errorf("menu = %s", got)
	}
	m := &appState{ctx: context.Background()}
	for _, action := range []string{"doctor", "status"} {
		if title, work, ok := m.reportFor(action); !ok || title == "" || work == nil {
			t.Errorf("action %q is not a report", action)
		}
	}
}

func TestUsageListsNoLogin(t *testing.T) {
	for _, spec := range usageSpecs {
		if spec.Name == "login" {
			t.Error("usage lists login: this server has no credentials")
		}
	}
}

func TestUpdateOptionsUseTheProjectIdentity(t *testing.T) {
	opts := updateOptions()
	if opts.Owner != domain.Owner || opts.Repo != domain.Repo || opts.AssetName == nil ||
		opts.AssetName("windows", "amd64") != "creality-slicer-mcp-windows-amd64.exe" {
		t.Errorf("update options = %+v", opts)
	}
}

func TestRefreshGuideHonoursDryRun(t *testing.T) {
	home := testHome(t)
	ours := skillPath(home, folderClaude)
	old := "---\nname: creality-slicer\nmetadata:\n  " + guide.Generator + "\n---\nold\n"
	if err := os.MkdirAll(filepath.Dir(ours), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ours, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runRefreshGuide(cli.Command{Args: []string{"--dry-run"}}); code != 0 {
		t.Fatal(code)
	}
	if got, _ := os.ReadFile(ours); string(got) != old {
		t.Error("--dry-run rewrote the copy")
	}
	if code := runRefreshGuide(cli.Command{}); code != 0 {
		t.Fatal(code)
	}
	if got, _ := os.ReadFile(ours); string(got) != guide.SkillMarkdown() {
		t.Error("the copy was not refreshed")
	}
}
