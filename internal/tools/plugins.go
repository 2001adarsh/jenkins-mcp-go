package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// pluginListTree is the `tree=` selector used against /pluginManager/api/json.
// The field set is intentionally tight to keep the response small even on
// instances with hundreds of plugins.
const pluginListTree = "plugins[shortName,longName,version,active,enabled,hasUpdate,pinned]"

// pluginListCap bounds the number of rendered rows. Hit by very large
// instances; the footer mentions the cap so the caller knows to narrow with
// name_filter.
const pluginListCap = 200

// GetPluginVersionsInput is the schema for get_plugin_versions.
type GetPluginVersionsInput struct {
	NameFilter      string `json:"name_filter,omitempty" jsonschema:"Case-insensitive RE2 regex matched against plugin shortName"`
	IncludeInactive bool   `json:"include_inactive,omitempty" jsonschema:"Include disabled or failed plugins. Default false — active-only."`
}

type pluginEntry struct {
	ShortName string `json:"shortName"`
	LongName  string `json:"longName"`
	Version   string `json:"version"`
	Active    bool   `json:"active"`
	Enabled   bool   `json:"enabled"`
	HasUpdate bool   `json:"hasUpdate"`
	Pinned    bool   `json:"pinned"`
}

// GetPluginVersions lists installed Jenkins plugins via
// /pluginManager/api/json. Active-only by default; pass include_inactive=true
// to surface disabled or failed plugins too. Optional name_filter narrows by
// case-insensitive RE2 against shortName. 403 (non-admin token) degrades to a
// clear hint rather than an error.
func (d Deps) GetPluginVersions(ctx context.Context, _ *mcp.CallToolRequest, in GetPluginVersionsInput) (*mcp.CallToolResult, any, error) {
	nameRe, err := compileFilter("name_filter", in.NameFilter)
	if err != nil {
		return nil, nil, err
	}

	body, err := d.Client.Get(ctx, "/pluginManager/api/json", map[string]string{
		"tree":  pluginListTree,
		"depth": "1",
	})
	if err != nil {
		if jenkins.IsHTTPStatus(err, http.StatusForbidden) {
			return textResult(
				"plugin status unavailable — token lacks admin permission on /pluginManager.",
			), nil, nil
		}
		return nil, nil, err
	}
	var resp struct {
		Plugins []pluginEntry `json:"plugins"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil, fmt.Errorf("parse /pluginManager/api/json: %w", err)
	}

	total := len(resp.Plugins)
	kept := make([]pluginEntry, 0, total)
	for _, p := range resp.Plugins {
		if !in.IncludeInactive && !p.Active {
			continue
		}
		if nameRe != nil && !nameRe.MatchString(p.ShortName) {
			continue
		}
		kept = append(kept, p)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].ShortName < kept[j].ShortName })

	truncated := false
	if len(kept) > pluginListCap {
		kept = kept[:pluginListCap]
		truncated = true
	}

	return textResult(renderPluginList(kept, total, in, truncated)), nil, nil
}

func renderPluginList(rows []pluginEntry, total int, in GetPluginVersionsInput, truncated bool) string {
	var out strings.Builder

	scope := "active"
	if in.IncludeInactive {
		scope = "all"
	}
	fmt.Fprintf(&out, "Installed plugins (%d of %d plugins shown, scope=%s",
		len(rows), total, scope)
	if in.NameFilter != "" {
		fmt.Fprintf(&out, ", name_filter=%q", in.NameFilter)
	}
	out.WriteString(")\n\n")

	if len(rows) == 0 {
		out.WriteString("(no plugins matched)\n")
		return out.String()
	}

	// Column widths chosen for typical Jenkins plugin metadata: shortNames
	// rarely exceed 30 chars, longNames can run long so they're truncated.
	const (
		shortW   = 30
		longW    = 36
		verW     = 12
		flagW    = 8
		updateW  = 9
		activeW  = 8
		enabledW = 8
	)
	fmt.Fprintf(&out, "%s  %s  %s  %s  %s",
		padRight("shortName", shortW),
		padRight("longName", longW),
		padRight("version", verW),
		padRight("pinned", flagW),
		padRight("hasUpdate", updateW),
	)
	if in.IncludeInactive {
		fmt.Fprintf(&out, "  %s  %s",
			padRight("active", activeW),
			padRight("enabled", enabledW),
		)
	}
	out.WriteString("\n")
	fmt.Fprintf(&out, "%s  %s  %s  %s  %s",
		padRight(strings.Repeat("-", shortW), shortW),
		padRight(strings.Repeat("-", longW), longW),
		padRight(strings.Repeat("-", verW), verW),
		padRight(strings.Repeat("-", flagW), flagW),
		padRight(strings.Repeat("-", updateW), updateW),
	)
	if in.IncludeInactive {
		fmt.Fprintf(&out, "  %s  %s",
			padRight(strings.Repeat("-", activeW), activeW),
			padRight(strings.Repeat("-", enabledW), enabledW),
		)
	}
	out.WriteString("\n")

	for _, p := range rows {
		fmt.Fprintf(&out, "%s  %s  %s  %s  %s",
			padRight(truncate(p.ShortName, shortW), shortW),
			padRight(truncate(p.LongName, longW), longW),
			padRight(truncate(p.Version, verW), verW),
			padRight(yesNo(p.Pinned), flagW),
			padRight(yesNo(p.HasUpdate), updateW),
		)
		if in.IncludeInactive {
			fmt.Fprintf(&out, "  %s  %s",
				padRight(yesNo(p.Active), activeW),
				padRight(yesNo(p.Enabled), enabledW),
			)
		}
		out.WriteString("\n")
	}

	if truncated {
		fmt.Fprintf(&out,
			"\n(truncated to %d rows — narrow with name_filter to see the rest)\n",
			pluginListCap)
	}
	return out.String()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
