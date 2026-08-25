package conversion

import (
	"testing"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestDecoderDecode(t *testing.T) {
	d := NewDecoder(newTestScheme(t))

	t.Run("registered type", func(t *testing.T) {
		obj, gvk, err := d.Decode([]byte(`{"apiVersion":"test.io/v1alpha1","kind":"Widget","metadata":{"name":"w1"},"sizeName":"large"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gvk.Kind != widgetKind || gvk.GroupVersion().String() != "test.io/v1alpha1" {
			t.Errorf("gvk = %s, want test.io/v1alpha1, Kind=Widget", gvk)
		}
		w, ok := obj.(*alphaWidget)
		if !ok {
			t.Fatalf("got %T, want *alphaWidget", obj)
		}
		if w.SizeName != "large" {
			t.Errorf("SizeName = %q, want %q", w.SizeName, "large")
		}
	})

	t.Run("unregistered type", func(t *testing.T) {
		if _, _, err := d.Decode([]byte(`{"apiVersion":"unregistered.io/v1","kind":"Nope"}`)); err == nil {
			t.Fatal("expected an error for an unregistered kind, got nil")
		}
	})

	t.Run("unparseable content leaves no gvk", func(t *testing.T) {
		_, gvk, err := d.Decode([]byte(`this is not json`))
		if err == nil {
			t.Fatal("expected an error for unparseable content, got nil")
		}
		if gvk != nil {
			t.Errorf("gvk = %v, want nil; handleConvertRequest relies on this being unusable", gvk)
		}
	})
}

func TestDecoderDecodeInto(t *testing.T) {
	d := NewDecoder(newTestScheme(t))

	t.Run("conversion review", func(t *testing.T) {
		var review apiextv1.ConversionReview
		body := []byte(`{"apiVersion":"apiextensions.k8s.io/v1","kind":"ConversionReview","request":{"uid":"abc-123","desiredAPIVersion":"test.io/v1beta1"}}`)

		if err := d.DecodeInto(body, &review); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if review.Request == nil {
			t.Fatal("Request is nil")
		}
		if review.Request.UID != "abc-123" {
			t.Errorf("UID = %q, want %q", review.Request.UID, "abc-123")
		}
		if review.Request.DesiredAPIVersion != "test.io/v1beta1" {
			t.Errorf("DesiredAPIVersion = %q, want %q", review.Request.DesiredAPIVersion, "test.io/v1beta1")
		}
	})

	t.Run("malformed content", func(t *testing.T) {
		var review apiextv1.ConversionReview
		if err := d.DecodeInto([]byte(`{"this is not": `), &review); err == nil {
			t.Fatal("expected an error for malformed content, got nil")
		}
	})
}
