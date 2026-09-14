package tfprovider

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"
)

func inputSettingsNotFound(err error) bool {
	var native *ws.Error
	return errors.As(err, &native) && native.Request == "GetInputSettings" && native.Code == 600
}

// Inventory can become stale before GetInputSettings runs. Absence waits do
// not need settings at all; inspect only exact UUIDs in the fresh inventory.
func inputPresent(ctx context.Context, cl requester, uuid string) (bool, error) {
	all, err := inputs(ctx, cl)
	if err != nil {
		return false, err
	}
	for _, in := range all {
		if in.UUID == uuid {
			return true, nil
		}
	}
	return false, nil
}

// Removing a placement is synchronous, releasing the final source reference is
// not. A hidden scene can retain its last render until another frame is drawn.
// Never force RemoveInput: another source/plugin may still legitimately own it.
func waitRemovedSource(ctx context.Context, cl requester, collection, uuid, parentUUID, parentName string) error {
	rendered := false
	return waitUntil(ctx, func() (bool, error) {
		if _, err := ensureCollection(ctx, cl, collection); err != nil {
			return false, err
		}
		present, err := inputPresent(ctx, cl, uuid)
		if err != nil {
			return false, err
		}
		if !present {
			return true, nil
		}
		refs, err := allSourceReferences(ctx, cl, uuid, "", -1)
		if err != nil {
			return false, err
		}
		if len(refs) > 0 {
			return false, fmt.Errorf("source %s still has references after placement removal: %v", uuid, refs)
		}
		if !rendered {
			parent, exists, err := sceneByUUID(ctx, cl, parentUUID)
			if err != nil {
				return false, err
			}
			if exists {
				if parent.Name != parentName {
					return false, fmt.Errorf("source deletion frame parent identity changed")
				}
				x, err := request(ctx, cl, "GetSourceScreenshot", map[string]any{"sourceUuid": parent.UUID, "imageFormat": "png", "imageWidth": 64, "imageHeight": 36})
				if err != nil {
					return false, fmt.Errorf("source deletion frame barrier: %w", err)
				}
				encoded, ok := x["imageData"].(string)
				if !ok {
					return false, fmt.Errorf("source deletion frame omitted imageData")
				}
				if comma := strings.IndexByte(encoded, ','); comma >= 0 {
					encoded = encoded[comma+1:]
				}
				data, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil || len(data) == 0 {
					return false, fmt.Errorf("source deletion frame returned invalid imageData")
				}
				if err := recordTrace(map[string]any{"event": "source-deletion-frame", "parent_scene_uuid": parent.UUID, "input_uuid": uuid}); err != nil {
					return false, err
				}
			}
			rendered = true
		}
		return false, nil
	})
}
