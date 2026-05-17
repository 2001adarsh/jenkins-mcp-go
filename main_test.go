package main

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTruthyEnv(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"Yes", true},
		{" yes ", true},
		{"0", false},
		{"false", false},
		{"", false},
		{"no", false},
		{"truthy", false},
	} {
		if got := truthyEnv(c.in); got != c.want {
			t.Errorf("truthyEnv(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

type emptyIn struct{}

func TestAddWriteTool_RespectsReadOnly(t *testing.T) {
	handler := func(_ context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
		return nil, nil, nil
	}

	rw := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	addWriteTool(rw, config{ReadOnly: false}, &mcp.Tool{Name: "mutate"}, handler)

	ro := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	addWriteTool(ro, config{ReadOnly: true}, &mcp.Tool{Name: "mutate"}, handler)

	// Probe the registered tool list via ListTools on each server's own
	// session; simpler proxy: re-register the same name and rely on the
	// SDK's behavior. Instead, use the lower-level Server.AddTool surface
	// by attempting a duplicate registration — if a tool with the same
	// name was already registered, AddTool replaces it silently, so we
	// can't detect via panic. Use the public ListTools instead.
	rwList := listToolNames(t, rw)
	if _, ok := rwList["mutate"]; !ok {
		t.Errorf("read-write server: expected 'mutate' tool registered, got %v", rwList)
	}
	roList := listToolNames(t, ro)
	if _, ok := roList["mutate"]; ok {
		t.Errorf("read-only server: expected 'mutate' to be skipped, got %v", roList)
	}
}

// listToolNames runs an in-process client against the server and returns the
// set of registered tool names.
func listToolNames(t *testing.T, srv *mcp.Server) map[string]struct{} {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = sess.Close() }()

	res, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := make(map[string]struct{}, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = struct{}{}
	}
	return names
}
