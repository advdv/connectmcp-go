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

// Package main demonstrates exposing a connect-go v2 service as MCP tools.
//
// In connect-go v2 a generated client and a service implementation share the
// same method signatures, so either can be handed to the generated
// Register<Service>Handler function:
//
//   - by default the tools call a connect server in this process through the
//     in-process transport, so its interceptors (auth, validation, tracing)
//     run for every tool call;
//   - with UPSTREAM_URL set, tool calls are forwarded to a remote connect
//     server instead.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/advdv/protoc-gen-go-mcp/pkg/runtime/gosdk"
	testdata "github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata"
	"github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata/testdataconnect"
	"github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata/testdatamcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil)) // stdout carries MCP

	var transport connect.Transport
	if upstream := os.Getenv("UPSTREAM_URL"); upstream != "" {
		// Forward tool calls to a remote connect server.
		transport = connecthttp.NewTransport(http.DefaultClient, upstream)
	} else {
		// Serve tool calls from a connect server in this process. The same
		// server could also be mounted on an http.ServeMux with connecthttp.
		server := connect.NewServer(logRPCs(logger))
		testdataconnect.RegisterTestServiceHandler(server, &testService{})
		transport = connectinprocess.New(server)
	}
	client := testdataconnect.NewTestServiceClient(connect.NewClient(transport))

	raw, s := gosdk.NewServer("Example connect-go v2 MCP server", "1.0.0")
	testdatamcp.RegisterTestServiceHandler(s, client)

	if err := raw.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

// logRPCs is a connect server interceptor; it sees every MCP tool call.
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
