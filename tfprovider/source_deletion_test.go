package tfprovider

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

type racedInputSettings struct {
	requester
	err error
}

func (r racedInputSettings) Req(ctx context.Context, kind string, data map[string]any) (map[string]any, error) {
	if kind == "GetInputSettings" {
		return nil, r.err
	}
	return r.requester.Req(ctx, kind, data)
}

func TestSourceDisappearsBetweenInventoryAndSettings(t *testing.T) {
	f := newGenericSourceFake()
	f.exists = true
	for _, tc := range []struct {
		name   string
		err    error
		absent bool
	}{
		{"missing UUID", &ws.Error{Request: "GetInputSettings", Code: 600}, true},
		{"wrapped missing UUID", fmt.Errorf("wrapped: %w", &ws.Error{Request: "GetInputSettings", Code: 600}), true},
		{"other status", &ws.Error{Request: "GetInputSettings", Code: 500}, false},
		{"other request", &ws.Error{Request: "GetInputDefaultSettings", Code: 600}, false},
		{"text only", errors.New("GetInputSettings failed: 600"), false},
		{"transport", errors.New("connection lost"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cl := racedInputSettings{requester: f, err: tc.err}
			for _, lookup := range []func(context.Context, requester, string) (inputObservation, bool, error){genericSourceByUUID, inputByUUID} {
				_, found, err := lookup(context.Background(), cl, "input-1")
				if found || (err == nil) != tc.absent {
					t.Fatalf("found=%v err=%v", found, err)
				}
			}
		})
	}
}

type delayedGroupRelease struct {
	base                         *groupSourceFixture
	removed, racedRef, keepAlive bool
	frames                       int
	cancel                       context.CancelFunc
}

func (f *delayedGroupRelease) Req(ctx context.Context, typ string, data map[string]any) (map[string]any, error) {
	if typ == "GetSourceScreenshot" {
		f.frames++
		if data["sourceUuid"] != groupFixtureSceneUUID {
			return nil, fmt.Errorf("wrong release frame scene")
		}
		if !f.keepAlive {
			f.base.input = false
		}
		if f.cancel != nil {
			f.cancel()
		}
		return map[string]any{"imageData": "data:image/png;base64,AQ=="}, nil
	}
	x, err := f.base.Req(ctx, typ, data)
	if typ == "CallVendorRequest" && data["requestType"] == "RemoveGroupChildPlacement" && err == nil {
		f.removed = true
		f.base.input = true
		f.base.extraReference = f.racedRef
		mapValue(mapValue(x["responseData"])["result"])["sourceStillExists"] = true
	}
	return x, err
}

func TestGroupSourceDeletionWaitsForReleaseAndRetriesMissingAnchor(t *testing.T) {
	for _, mode := range []string{"delayed", "retry", "new-reference", "still-held"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &delayedGroupRelease{base: &groupSourceFixture{input: true, parent: true, child: mode != "retry", settings: map[string]any{}}, racedRef: mode == "new-reference", keepAlive: mode == "still-held"}
			if f.keepAlive {
				f.cancel = cancel
			}
			m := groupSourceTestModel()
			state := groupFixtureSchemaAndState(t, ctx, groupSourceTestSchema(ctx), &m)
			resp := resource.DeleteResponse{State: state}
			(&groupSourceResource{client: &client{testRequester: f}}).Delete(ctx, resource.DeleteRequest{State: state}, &resp)
			wantErr := f.racedRef || f.keepAlive
			if resp.Diagnostics.HasError() != wantErr {
				t.Fatal(resp.Diagnostics)
			}
			if !wantErr && (f.base.input || f.frames != 1) {
				t.Fatal("delayed source was not released by its parent frame")
			}
			if mode == "retry" && f.removed {
				t.Fatal("missing anchor removed twice")
			}
			if f.racedRef && (!f.base.input || f.frames != 0) {
				t.Fatal("new shared reference disturbed")
			}
			for _, c := range f.base.calls {
				if c.typ == "RemoveInput" {
					t.Fatal("forced source lifetime deletion")
				}
			}
		})
	}
}

type delayedSceneRelease struct {
	base   *genericSourceFake
	frames int
}

func (f *delayedSceneRelease) Req(ctx context.Context, typ string, data map[string]any) (map[string]any, error) {
	if typ == "GetSourceScreenshot" {
		f.frames++
		f.base.exists = false
		return map[string]any{"imageData": "AQ=="}, nil
	}
	x, err := f.base.Req(ctx, typ, data)
	if typ == "RemoveSceneItem" && err == nil {
		f.base.exists = true
	}
	return x, err
}
func TestGenericSourceDeletionWaitsForFinalFrame(t *testing.T) {
	f := &delayedSceneRelease{base: newGenericSourceFake()}
	f.base.exists = true
	f.base.anchor = true
	m := genericSourceTestModel(f.base)
	if err := deleteGenericSource(context.Background(), f, m); err != nil {
		t.Fatal(err)
	}
	if f.frames != 1 || f.base.exists {
		t.Fatal("source lifetime not settled")
	}
}
