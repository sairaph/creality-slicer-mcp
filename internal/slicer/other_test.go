//go:build !windows

package slicer

import (
	"context"
	"testing"
)

func TestUnsupportedPlatformReportsReason(t *testing.T) {
	in, err := Detect(context.Background(), Options{})
	if err != nil || in.Found || in.ReasonCode != ReasonUnsupportedPlatform || in.Reason == "" {
		t.Errorf("%+v %v", in, err)
	}
}
