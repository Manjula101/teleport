/*
 * Teleport
 * Copyright (C) 2025  Gravitational, Inc.
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package mcputils

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	sampleNotificationJSON = []byte(`{
  "jsonrpc": "2.0",
  "method": "notifications/message",
  "params": {
    "level": "error",
    "logger": "database",
    "data": {
      "error": "Connection failed",
      "details": {
        "host": "localhost",
        "port": 5432
      }
    }
  }
}`)
	sampleToolsCallRequestJSON = []byte(`{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "get_weather",
    "arguments": {
      "location": "New York"
    }
  }
}`)
	sampleInitializeRequestJSON = []byte(`{
  "jsonrpc": "2.0",
  "id": "some-uuid",
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-06-18",
    "capabilities": {},
    "clientInfo": {
      "name": "ExampleClient",
      "version": "1.0.0"
    }
  }
}`)

	sampleResponseJSON = []byte(`{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "tools": [
      {
        "name": "get_weather",
        "description": "Get current weather information for a location",
        "inputSchema": {
          "type": "object",
          "properties": {
            "location": {
              "type": "string",
              "description": "City name or zip code"
            }
          },
          "required": ["location"]
        }
      }
    ],
    "nextCursor": "next-page-cursor"
  }
}`)
)

func TestJSONRPCNotification(t *testing.T) {
	var base BaseJSONRPCMessage
	require.NoError(t, json.Unmarshal(sampleNotificationJSON, &base))
	assert.True(t, base.IsNotification())
	assert.False(t, base.IsRequest())
	assert.False(t, base.IsResponse())

	m := base.MakeNotification()
	require.NotNil(t, m)
	assert.Equal(t, mcp.MCPMethod("notifications/message"), m.Method)
	assert.Len(t, base.Params, 3)

	outputJSON, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	assert.JSONEq(t, string(sampleNotificationJSON), string(outputJSON))
}

func TestJSONRPCRequest(t *testing.T) {
	tests := []struct {
		name         string
		inputJSON    []byte
		expectMethod mcp.MCPMethod
		expectID     string
		extraChecks  func(*testing.T, *JSONRPCRequest)
	}{
		{
			name:         "tools/call",
			inputJSON:    sampleToolsCallRequestJSON,
			expectMethod: mcp.MethodToolsCall,
			expectID:     "int64:2",
			extraChecks: func(t *testing.T, r *JSONRPCRequest) {
				t.Helper()
				name, ok := r.Params.GetName()
				assert.True(t, ok)
				assert.Equal(t, "get_weather", name)
			},
		},
		{
			name:         "initialize",
			inputJSON:    sampleInitializeRequestJSON,
			expectMethod: mcp.MethodInitialize,
			expectID:     "string:some-uuid",
			extraChecks: func(t *testing.T, r *JSONRPCRequest) {
				t.Helper()
				params, err := r.GetInitializeParams()
				require.NoError(t, err)
				assert.Equal(t, &mcp.InitializeParams{
					ProtocolVersion: "2025-06-18",
					Capabilities:    mcp.ClientCapabilities{},
					ClientInfo: mcp.Implementation{
						Name:    "ExampleClient",
						Version: "1.0.0",
					},
				}, params)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var base BaseJSONRPCMessage
			require.NoError(t, json.Unmarshal(test.inputJSON, &base))
			assert.False(t, base.IsNotification())
			assert.True(t, base.IsRequest())
			assert.False(t, base.IsResponse())

			m := base.MakeRequest()
			require.NotNil(t, m)
			assert.Equal(t, test.expectMethod, m.Method)
			assert.Equal(t, test.expectID, m.ID.String())
			test.extraChecks(t, m)

			outputJSON, err := json.MarshalIndent(m, "", "  ")
			require.NoError(t, err)
			assert.JSONEq(t, string(test.inputJSON), string(outputJSON))
		})
	}
}

func TestJSONRPCResponse(t *testing.T) {
	var base BaseJSONRPCMessage
	require.NoError(t, json.Unmarshal(sampleResponseJSON, &base))
	assert.False(t, base.IsNotification())
	assert.False(t, base.IsRequest())
	assert.True(t, base.IsResponse())

	m := base.MakeResponse()
	require.NotNil(t, m)
	assert.Equal(t, "int64:2", m.ID.String())

	outputJSON, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	assert.JSONEq(t, string(sampleResponseJSON), string(outputJSON))

	toolList, err := m.GetListToolResult()
	require.NoError(t, err)
	require.Equal(t, &mcp.ListToolsResult{
		PaginatedResult: mcp.PaginatedResult{
			NextCursor: "next-page-cursor",
		},
		Tools: []mcp.Tool{{
			Name:        "get_weather",
			Description: "Get current weather information for a location",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]any{
					"location": map[string]any{
						"type":        "string",
						"description": "City name or zip code",
					},
				},
				Required: []string{"location"},
			},
		}},
	}, toolList)
}
