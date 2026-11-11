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

// Package main serves a service implementation as MCP tools over stdio.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/advdv/protoc-gen-go-mcp/pkg/runtime/gosdk"
	testdata "github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata"
	"github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata/testdatamcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	// raw is the *mcp.Server for transport setup, s is the runtime.MCPServer
	// the generated code registers tools on.
	raw, s := gosdk.NewServer("Example auto-generated MCP server", "1.0.0")

	// Register the generated MCP handlers.
	testdatamcp.RegisterTestServiceHandler(s, &testServer{})

	if err := raw.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

type testServer struct{}

func (t *testServer) CreateItem(ctx context.Context, in *testdata.CreateItemRequest) (*testdata.CreateItemResponse, error) {
	return &testdata.CreateItemResponse{
		Id: "item-123",
	}, nil
}

func (t *testServer) GetItem(ctx context.Context, in *testdata.GetItemRequest) (*testdata.GetItemResponse, error) {
	return &testdata.GetItemResponse{
		Item: &testdata.Item{
			Id:   in.GetId(),
			Name: "Retrieved item",
		},
	}, nil
}

func (t *testServer) ProcessWellKnownTypes(ctx context.Context, in *testdata.ProcessWellKnownTypesRequest) (*testdata.ProcessWellKnownTypesResponse, error) {
	return &testdata.ProcessWellKnownTypesResponse{
		Message: "Processed well-known types",
	}, nil
}

func (t *testServer) TestValidation(ctx context.Context, in *testdata.TestValidationRequest) (*testdata.TestValidationResponse, error) {
	return &testdata.TestValidationResponse{
		Success: true,
		Message: "Validation test completed",
	}, nil
}
