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

package appaccess

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gravitational/trace"
	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpclienttransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport/lib/client"
	clientmcp "github.com/gravitational/teleport/lib/client/mcp"
	libmcp "github.com/gravitational/teleport/lib/srv/mcp"
	"github.com/gravitational/teleport/lib/utils/mcptest"
)

func testMCP(pack *Pack, t *testing.T) {
	t.Run("stdio no server found", func(t *testing.T) {
		testMCPDialStdioNoServerFound(t, pack)
	})

	t.Run("stdio success", func(t *testing.T) {
		testMCPDialStdio(t, pack)
	})

	t.Run("stdio to sse success", func(t *testing.T) {
		testMCPDialStdioToSSE(t, pack, "test-sse")
	})

	t.Run("proxy streamable HTTP success", func(t *testing.T) {
		testMCPProxyStreamableHTTP(t, pack, "test-http")
	})

	t.Run("stdio to streamable HTTP success", func(t *testing.T) {
		testMCPStdioToStreamableHTTP(t, pack, "test-http")
	})
}

func testMCPDialStdioNoServerFound(t *testing.T, pack *Pack) {
	require.NoError(t, pack.tc.SaveProfile(false))

	dialer := client.NewMCPServerDialer(pack.tc, "not-found")
	_, err := dialer.Dial(t.Context())
	require.Error(t, err)
}

func testMCPDialStdio(t *testing.T, pack *Pack) {
	require.NoError(t, pack.tc.SaveProfile(false))

	dialer := client.NewMCPServerDialer(pack.tc, libmcp.DemoServerName)
	serverConn, err := dialer.Dial(t.Context())
	require.NoError(t, err)

	ctx := t.Context()
	stdioClient := mcptest.NewStdioClientFromConn(t, serverConn)

	_, err = mcptest.InitializeClient(ctx, stdioClient)
	require.NoError(t, err)

	listTools, err := stdioClient.ListTools(ctx, mcp.ListToolsRequest{})
	require.NoError(t, err)
	require.Len(t, listTools.Tools, 3)
}

func testMCPDialStdioToSSE(t *testing.T, pack *Pack, appName string) {
	require.NoError(t, pack.tc.SaveProfile(false))

	dialer := client.NewMCPServerDialer(pack.tc, appName)
	serverConn, err := dialer.Dial(t.Context())
	require.NoError(t, err)

	ctx := t.Context()
	stdioClient := mcptest.NewStdioClientFromConn(t, serverConn)

	_, err = mcptest.InitializeClient(ctx, stdioClient)
	require.NoError(t, err)

	mcptest.MustCallServerTool(t, ctx, stdioClient)
}

func testMCPProxyStreamableHTTP(t *testing.T, pack *Pack, appName string) {
	require.NoError(t, pack.tc.SaveProfile(false))

	ctx := t.Context()
	dialer := client.NewMCPServerDialer(pack.tc, appName)
	mcpClientTransport, err := mcpclienttransport.NewStreamableHTTP(
		"https://"+pack.rootCluster.Web,
		mcpclienttransport.WithHTTPBasicClient(&http.Client{
			Transport: &http.Transport{
				DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.Dial(t.Context())
				},
			},
		}),
	)
	require.NoError(t, err)
	client := mcpclient.NewClient(mcpClientTransport)
	require.NoError(t, client.Start(ctx))
	defer client.Close()

	// Initialize client and call a tool.
	_, err = mcptest.InitializeClient(ctx, client)
	require.NoError(t, err)
	mcptest.MustCallServerTool(t, ctx, client)
}

func testMCPStdioToStreamableHTTP(t *testing.T, pack *Pack, appName string) {
	require.NoError(t, pack.tc.SaveProfile(false))

	fromServer, toServer := net.Pipe()
	t.Cleanup(func() {
		assert.NoError(t, trace.NewAggregate(fromServer.Close(), toServer.Close()))
	})

	ctx := t.Context()
	dialer := client.NewMCPServerDialer(pack.tc, appName)
	proxyErrChan := make(chan error, 1)
	go func() {
		err := clientmcp.ProxyStdioConn(
			t.Context(),
			clientmcp.ProxyStdioConnConfig{
				ClientStdio: toServer,
				GetApp:      dialer.GetApp,
				DialServer:  dialer.Dial,
			},
		)
		proxyErrChan <- err
	}()

	stdioClient := mcptest.NewStdioClientFromConn(t, fromServer)
	_, err := mcptest.InitializeClient(ctx, stdioClient)
	require.NoError(t, err)
	mcptest.MustCallServerTool(t, ctx, stdioClient)

	// Shut client done and wait for proxy to finish.
	require.NoError(t, stdioClient.Close())
	select {
	case proxyErr := <-proxyErrChan:
		require.NoError(t, proxyErr)
	case <-time.After(time.Second * 5):
		require.FailNow(t, "proxy connection timed out")
	}
}
