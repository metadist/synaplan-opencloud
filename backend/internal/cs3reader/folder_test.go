package cs3reader

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	gateway "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
)

func TestStatResource(t *testing.T) {
	folder := &provider.ResourceInfo{
		Id:   &provider.ResourceId{StorageId: "s", SpaceId: "sp", OpaqueId: "f"},
		Name: "Inbox", Path: "./Inbox", Etag: "e1",
		Type: provider.ResourceType_RESOURCE_TYPE_CONTAINER,
	}
	authed := revactx.ContextSetToken(context.Background(), "reva-ada")
	cases := []struct {
		name      string
		ctx       context.Context
		stat      *provider.StatResponse
		want      Resource
		wantErr   bool
		notFound  bool
		wantToken string
	}{
		{
			name:      "stat a folder",
			ctx:       authed,
			stat:      &provider.StatResponse{Status: status(rpc.Code_CODE_OK), Info: folder},
			want:      Resource{ID: "s$sp!f", Name: "Inbox", Path: "Inbox", Etag: "e1", IsFolder: true},
			wantToken: "reva-ada",
		},
		{
			name:     "a gone resource is ErrNotFound",
			ctx:      authed,
			stat:     &provider.StatResponse{Status: status(rpc.Code_CODE_NOT_FOUND)},
			wantErr:  true,
			notFound: true,
		},
		{
			name:    "without a reva token nothing is asked",
			ctx:     context.Background(),
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gw := &fakeGateway{stats: []*provider.StatResponse{tc.stat}}
			got, err := readerWith(gw, "").StatResource(tc.ctx, "s$sp!f")
			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				if errors.Is(err, ErrNotFound) != tc.notFound {
					t.Fatalf("err %v, notFound = %v", err, tc.notFound)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if gw.gotToken != tc.wantToken {
				t.Fatalf("the gateway got token %q", gw.gotToken)
			}
		})
	}
}

func TestList(t *testing.T) {
	file := &provider.ResourceInfo{
		Id:   &provider.ResourceId{StorageId: "s", SpaceId: "sp", OpaqueId: "a"},
		Name: "a.txt", Path: "./Inbox/a.txt", Etag: "e2", MimeType: "text/plain", Size: 5,
		Type: provider.ResourceType_RESOURCE_TYPE_FILE,
	}
	authed := revactx.ContextSetToken(context.Background(), "reva-ada")
	cases := []struct {
		name     string
		ctx      context.Context
		list     *provider.ListContainerResponse
		want     []Resource
		wantErr  bool
		notFound bool
	}{
		{
			name: "list a folder",
			ctx:  authed,
			list: &provider.ListContainerResponse{Status: status(rpc.Code_CODE_OK), Infos: []*provider.ResourceInfo{file}},
			want: []Resource{{ID: "s$sp!a", Name: "a.txt", Path: "Inbox/a.txt", Etag: "e2", MimeType: "text/plain", Size: 5}},
		},
		{
			name:     "a gone folder is ErrNotFound",
			ctx:      authed,
			list:     &provider.ListContainerResponse{Status: status(rpc.Code_CODE_NOT_FOUND)},
			wantErr:  true,
			notFound: true,
		},
		{
			name:    "any other failure is not a deletion",
			ctx:     authed,
			list:    &provider.ListContainerResponse{Status: status(rpc.Code_CODE_INTERNAL)},
			wantErr: true,
		},
		{
			name:    "without a reva token nothing is asked",
			ctx:     context.Background(),
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gw := &fakeGateway{list: tc.list}
			got, err := readerWith(gw, "").List(tc.ctx, "s$sp!f")
			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				if errors.Is(err, ErrNotFound) != tc.notFound {
					t.Fatalf("err %v, notFound = %v", err, tc.notFound)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestOpenVersion(t *testing.T) {
	dataSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello")
	}))
	t.Cleanup(dataSrv.Close)

	fileAt := func(etag string) *provider.StatResponse {
		return &provider.StatResponse{
			Status: status(rpc.Code_CODE_OK),
			Info: &provider.ResourceInfo{
				Id:       &provider.ResourceId{StorageId: "s", SpaceId: "sp", OpaqueId: "a"},
				Name:     "a.txt",
				Path:     "./Inbox/a.txt",
				Etag:     etag,
				MimeType: "text/plain",
				Size:     5,
				Type:     provider.ResourceType_RESOURCE_TYPE_FILE,
				Space:    &provider.StorageSpace{Root: &provider.ResourceId{StorageId: "s", SpaceId: "sp", OpaqueId: "sp"}},
			},
		}
	}
	authed := revactx.ContextSetToken(context.Background(), "reva-ada")

	cases := []struct {
		name    string
		etag    string
		stats   []*provider.StatResponse
		wantErr error
	}{
		{
			name:  "matching version opens and verifies after download starts",
			etag:  "v1",
			stats: []*provider.StatResponse{fileAt("v1"), fileAt("v1")},
		},
		{
			name:    "changed before opening is ErrChanged without downloading",
			etag:    "v1",
			stats:   []*provider.StatResponse{fileAt("v2")},
			wantErr: ErrChanged,
		},
		{
			name:    "changed between first stat and download is ErrChanged",
			etag:    "v1",
			stats:   []*provider.StatResponse{fileAt("v1"), fileAt("v2")},
			wantErr: ErrChanged,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gw := &fakeGateway{
				stats: tc.stats,
				download: &gateway.InitiateFileDownloadResponse{
					Status: status(rpc.Code_CODE_OK),
					Protocols: []*gateway.FileDownloadProtocol{{
						Protocol:         "simple",
						DownloadEndpoint: dataSrv.URL,
						Token:            "dl-token",
					}},
				},
			}
			f, err := readerWith(gw, "").OpenVersion(authed, "s$sp!a", tc.etag)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Body.Close() }()
			body, _ := io.ReadAll(f.Body)
			if string(body) != "hello" || f.Etag != "v1" || f.Path != "Inbox/a.txt" {
				t.Fatalf("got body=%q etag=%q path=%q", string(body), f.Etag, f.Path)
			}
		})
	}
}
