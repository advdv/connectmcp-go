// Copyright 2025 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package main serves a connect-go v2 server over HTTP twice: as a regular
// connect API, and as MCP tools under /mcp.
//
// Both entry points dispatch to the same *connect.Server, so its interceptors
// (auth, validation, tracing) run for RPCs and tool calls alike.
//
// With UPSTREAM_URL set, the service is not implemented here: a generated
// connect client is registered as the handler instead, which forwards every
// call to that remote connect server.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/advdv/protoc-gen-go-mcp/pkg/connectmcp"
	testdata "github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata"
	"github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata/testdataconnect"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	var service testdataconnect.TestServiceHandler = &testService{}
	if upstream := os.Getenv("UPSTREAM_URL"); upstream != "" {
		// A generated client has the handler's method signatures.
		service = testdataconnect.NewTestServiceClient(connect.NewClient(
			connecthttp.NewTransport(http.DefaultClient, upstream)))
	}

	server := connect.NewServer(logRPCs(logger))
	testdataconnect.RegisterTestServiceHandler(server, service)

	// Every method carrying the (mcp.v1.tool_description) option is a tool.
	mcpHandler, err := connectmcp.NewHandler(server,
		connectmcp.WithServerInfo("Example connect-go v2 MCP server", "1.0.0"))
	if err != nil {
		logger.Error("create MCP handler", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	mux.Handle("/mcp", mcpHandler)

	addr := "localhost:8080"
	logger.Info("listening", "addr", addr, "mcp", "http://"+addr+"/mcp")
	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

func logRPCs(logger *slog.Logger) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			err := next(ctx, spec, stream)
			logger.InfoContext(ctx, "rpc completed", "procedure", spec.Procedure, "error", err)
			return err
		}
	}
}

// testService implements testdataconnect.TestServiceHandler.
type testService struct {
	testdataconnect.UnimplementedTestServiceHandler
}

func (*testService) CreateItem(_ context.Context, req *testdata.CreateItemRequest) (*testdata.CreateItemResponse, error) {
	if req.GetName() == "" {
		// Connect errors reach the model as a structured tool error.
		return nil, connect.NewError(connect.CodeInvalidArgument, "name is required")
	}
	return &testdata.CreateItemResponse{Id: "item-123"}, nil
}

func (*testService) GetItem(_ context.Context, req *testdata.GetItemRequest) (*testdata.GetItemResponse, error) {
	if req.GetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "id is required")
	}
	return &testdata.GetItemResponse{
		Item: &testdata.Item{Id: req.GetId(), Name: "Retrieved item"},
	}, nil
}
