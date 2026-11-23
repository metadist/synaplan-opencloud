package cs3reader

import (
	"context"
	"testing"

	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
)

func TestTagSynaplanFileRecordsTheId(t *testing.T) {
	gw := &fakeGateway{}
	r := readerWith(gw, "")
	ctx := revactx.ContextSetToken(context.Background(), "reva")
	if err := r.TagSynaplanFile(ctx, "storage$space!file", 42); err != nil {
		t.Fatal(err)
	}
	if gw.meta["synaplan-file-id"] != "42" {
		t.Fatalf("metadata %v", gw.meta)
	}
}

func TestTagSynaplanFileRejectsNoId(t *testing.T) {
	r := readerWith(&fakeGateway{}, "")
	err := r.TagSynaplanFile(revactx.ContextSetToken(context.Background(), "reva"), "storage$space!file", 0)
	if err == nil {
		t.Fatal("expected an error")
	}
}
