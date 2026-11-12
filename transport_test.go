package connectmcp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/advdv/connectmcp-go"
	testdata "github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata"
	"github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata/testdataconnect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
)

// transportItems implements TestService for the transport tests.
type transportItems struct {
	testdataconnect.UnimplementedTestServiceHandler
	created []*testdata.CreateItemRequest
}

func (s *transportItems) CreateItem(_ context.Context, req *testdata.CreateItemRequest) (*testdata.CreateItemResponse, error) {
	s.created = append(s.created, req)
	return &testdata.CreateItemResponse{Id: "created-" + req.GetName()}, nil
}

func (*transportItems) GetItem(_ context.Context, req *testdata.GetItemRequest) (*testdata.GetItemResponse, error) {
	detail, err := connectproto.NewErrorDetail(&errdetails.ResourceInfo{ResourceType: "item", ResourceName: req.GetId()})
	if err != nil {
		return nil, err
	}
	return nil, connect.NewError(connect.CodeNotFound, "no such item").WithDetail(detail)
}

// transportEdgeCases answers OneofRecursive with the tree it was sent.
type transportEdgeCases struct {
	testdataconnect.UnimplementedEdgeCaseServiceHandler
}

func (transportEdgeCases) OneofRecursive(_ context.Context, req *testdata.OneofRecursiveRequest) (*testdata.OneofRecursiveResponse, error) {
	return &testdata.OneofRecursiveResponse{Result: &testdata.OneofRecursiveResponse_Tree{Tree: req.GetTree()}}, nil
}

// tree builds a TreeNode nested deeper than the schemas expand.
func tree(depth int) *testdata.TreeNode {
	node := &testdata.TreeNode{Value: "leaf"}
	for range depth {
		node = &testdata.TreeNode{Value: "node", Children: []*testdata.TreeNode{node}}
	}
	return node
}

// TestNewTransport drives generated connect clients through NewTransport into
// a server served by NewHandler, over real HTTP: the client-level view of the
// MCP surface, as connecthttp.NewTransport is of the Connect surface.
func TestNewTransport(t *testing.T) {
	var (
		mu       sync.Mutex
		seen     []*connect.CallInfo
		requests []*http.Request
	)
	record := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			info, _ := connect.CallInfoForServerContext(ctx)
			seen = append(seen, info)
			return next(ctx, spec, stream)
		}
	}
	items := &transportItems{}
	server := connect.NewServer(record)
	testdataconnect.RegisterTestServiceHandler(server, items)
	testdataconnect.RegisterEdgeCaseServiceHandler(server, transportEdgeCases{})

	handler, err := connectmcp.NewHandler(server)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r)
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)

	transport := connectmcp.NewTransport(httpServer.Client(), httpServer.URL)
	t.Cleanup(func() { _ = transport.Close() })
	itemsClient := testdataconnect.NewTestServiceClient(connect.NewClient(transport))
	edgeClient := testdataconnect.NewEdgeCaseServiceClient(connect.NewClient(transport))

	t.Run("request and response messages", func(t *testing.T) {
		g := NewWithT(t)
		seen = nil
		req := &testdata.CreateItemRequest{
			Name:     "Widget",
			Labels:   map[string]string{"env": "prod"},
			ItemType: &testdata.CreateItemRequest_Product{Product: &testdata.ProductDetails{Price: 9.5, Quantity: 2}},
		}
		res, err := itemsClient.CreateItem(t.Context(), req)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res.GetId()).To(Equal("created-Widget"))
		g.Expect(proto.Equal(items.created[len(items.created)-1], req)).To(BeTrue())

		g.Expect(seen).To(HaveLen(1))
		g.Expect(seen[0].Protocol).To(Equal(connectmcp.Protocol))
		g.Expect(seen[0].Spec.Procedure).To(Equal("/testdata.TestService/CreateItem"))
	})

	// Oneof wrappers and recursion-depth placeholders are the two shapes that
	// are not plain protojson, in arguments and in results.
	t.Run("oneof and deep recursion", func(t *testing.T) {
		g := NewWithT(t)
		req := &testdata.OneofRecursiveRequest{Node: &testdata.OneofRecursiveRequest_Tree{Tree: tree(6)}}
		res, err := edgeClient.OneofRecursive(t.Context(), req)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(proto.Equal(res.GetTree(), req.GetTree())).To(BeTrue())
	})

	t.Run("error with details", func(t *testing.T) {
		g := NewWithT(t)
		_, err := itemsClient.GetItem(t.Context(), &testdata.GetItemRequest{Id: "item-1"})
		cerr, ok := errors.AsType[*connect.Error](err)
		g.Expect(ok).To(BeTrue(), "%v", err)
		g.Expect(cerr.Code()).To(Equal(connect.CodeNotFound))
		g.Expect(cerr.Message()).To(Equal("no such item"))
		g.Expect(cerr.IsRemote()).To(BeTrue())
		g.Expect(cerr.Details()).To(HaveLen(1))
		info := &errdetails.ResourceInfo{}
		g.Expect(proto.Unmarshal(cerr.Details()[0].Value, info)).To(Succeed())
		g.Expect(info.GetResourceName()).To(Equal("item-1"))

		// A method the service does not implement fails as over connect.
		_, err = itemsClient.TestValidation(t.Context(), &testdata.TestValidationRequest{})
		g.Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnimplemented))
	})

	t.Run("request headers of one call", func(t *testing.T) {
		g := NewWithT(t)
		seen = nil
		mu.Lock()
		requests = nil
		mu.Unlock()

		ctx, info := connect.NewClientContext(t.Context())
		info.RequestHeader().Set("Authorization", "Bearer secret")
		info.RequestHeader().Set("Content-Type", "text/plain") // the MCP client's own header wins
		_, err := itemsClient.CreateItem(ctx, &testdata.CreateItemRequest{Name: "a"})
		g.Expect(err).ToNot(HaveOccurred())
		_, err = itemsClient.CreateItem(t.Context(), &testdata.CreateItemRequest{Name: "b"})
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(seen).To(HaveLen(2))
		g.Expect(seen[0].RequestHeader().Get("Authorization")).To(Equal("Bearer secret"))
		g.Expect(seen[0].RequestHeader().Get("Content-Type")).To(Equal("application/json"))
		g.Expect(seen[1].RequestHeader().Has("Authorization")).To(BeFalse(), "a call's headers must not stick to the session")
	})

	t.Run("tool that is not served", func(t *testing.T) {
		g := NewWithT(t)
		// ToolNamingService is not registered on the server.
		client := testdataconnect.NewToolNamingServiceClient(connect.NewClient(transport))
		_, err := client.GetDashboard(t.Context(), &testdata.ToolNamingRequest{})
		g.Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnavailable))
		g.Expect(err.Error()).To(ContainSubstring("unknown tool"))
	})

	t.Run("canceled", func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := itemsClient.CreateItem(ctx, &testdata.CreateItemRequest{Name: "a"})
		g.Expect(connect.CodeOf(err)).To(Equal(connect.CodeCanceled))
	})

	t.Run("closed", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(transport.Close()).To(Succeed())
		g.Expect(transport.Close()).To(Succeed())
		_, err := itemsClient.CreateItem(t.Context(), &testdata.CreateItemRequest{Name: "a"})
		g.Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnavailable))

		// Closing ended the session on the server.
		mu.Lock()
		defer mu.Unlock()
		g.Expect(requests[len(requests)-1].Method).To(Equal(http.MethodDelete))
	})
}

// TestNewTransport_FirstCallHeaders checks that the headers of the call that
// opens the session are sent with that call only, not with initialize.
func TestNewTransport_FirstCallHeaders(t *testing.T) {
	g := NewWithT(t)
	server := connect.NewServer()
	testdataconnect.RegisterTestServiceHandler(server, &transportItems{})
	handler, err := connectmcp.NewHandler(server)
	g.Expect(err).ToNot(HaveOccurred())

	var (
		mu   sync.Mutex
		auth []string
	)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)

	transport := connectmcp.NewTransport(nil, httpServer.URL)
	client := testdataconnect.NewTestServiceClient(connect.NewClient(transport))
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer secret")
	_, err = client.CreateItem(ctx, &testdata.CreateItemRequest{Name: "a"})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(transport.Close()).To(Succeed())

	mu.Lock()
	defer mu.Unlock()
	// server/discover, initialize, notifications/initialized, tools/call, DELETE.
	g.Expect(auth).To(Equal([]string{"", "", "", "Bearer secret", ""}))
}

// TestNewTransport_RequestTooLarge checks that a request message over the
// server's limit is RESOURCE_EXHAUSTED whether the server refuses the
// arguments (a tool error) or the HTTP body that carries them (HTTP 413).
func TestNewTransport_RequestTooLarge(t *testing.T) {
	const limit = 1024
	for name, opts := range map[string]*mcp.StreamableHTTPOptions{
		"default":        nil,
		"stateless json": {Stateless: true, JSONResponse: true},
	} {
		t.Run(name, func(t *testing.T) {
			items := &transportItems{}
			server := connect.NewServer()
			testdataconnect.RegisterTestServiceHandler(server, items)
			handler, err := connectmcp.NewHandler(server,
				connectmcp.WithReadMaxBytes(limit), connectmcp.WithStreamableHTTPOptions(opts))
			if err != nil {
				t.Fatal(err)
			}
			httpServer := httptest.NewServer(handler)
			t.Cleanup(httpServer.Close)

			for name, tc := range map[string]struct {
				size    int
				message string
			}{
				"arguments over the limit": {size: limit, message: "message is larger than configured max 1024"},
				"body over the limit":      {size: limit + connectmcp.EnvelopeMaxBytes, message: "Request Entity Too Large"},
			} {
				t.Run(name, func(t *testing.T) {
					g := NewWithT(t)
					transport := connectmcp.NewTransport(nil, httpServer.URL)
					t.Cleanup(func() { _ = transport.Close() })
					client := testdataconnect.NewTestServiceClient(connect.NewClient(transport))

					_, err := client.CreateItem(t.Context(), &testdata.CreateItemRequest{Name: strings.Repeat("x", tc.size)})
					g.Expect(connect.CodeOf(err)).To(Equal(connect.CodeResourceExhausted), "%v", err)
					g.Expect(err).To(MatchError(ContainSubstring(tc.message)))
					g.Expect(items.created).To(BeEmpty())
				})
			}
		})
	}
}
