package conversion

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// mustNotPanic runs fn and turns a panic into a test failure, so that one
// crashing handler does not abort the rest of the suite.
func mustNotPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handler panicked: %v", r)
		}
	}()
	fn()
}

func serve(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/convert", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mustNotPanic(t, func() { h.ServeHTTP(rec, req) })
	return rec
}

// decodeReview pulls the ConversionReview back out of the recorded response.
// ConvertedObjects stay raw so tests can assert on the wire format.
type recordedReview struct {
	Response struct {
		UID              types.UID         `json:"uid"`
		ConvertedObjects []json.RawMessage `json:"convertedObjects"`
		Result           metav1.Status     `json:"result"`
	} `json:"response"`
}

func decodeReview(t *testing.T, rec *httptest.ResponseRecorder) recordedReview {
	t.Helper()

	var got recordedReview
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response %q: %v", rec.Body.String(), err)
	}
	return got
}

func TestServeHTTPConvertsSpokeToSpoke(t *testing.T) {
	h := newTestHandler(t)

	body := `{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind": "ConversionReview",
		"request": {
			"uid": "abc-123",
			"desiredAPIVersion": "test.io/v1beta1",
			"objects": [
				{"apiVersion":"test.io/v1alpha1","kind":"Widget","metadata":{"name":"w1"},"sizeName":"large"}
			]
		}
	}`

	rec := serve(t, h, body)
	got := decodeReview(t, rec)

	if got.Response.Result.Status != metav1.StatusSuccess {
		t.Fatalf("expected success, got %+v", got.Response.Result)
	}
	if got.Response.UID != "abc-123" {
		t.Errorf("UID = %q, want %q", got.Response.UID, "abc-123")
	}
	if len(got.Response.ConvertedObjects) != 1 {
		t.Fatalf("expected 1 converted object, got %d", len(got.Response.ConvertedObjects))
	}

	var out betaWidget
	if err := json.Unmarshal(got.Response.ConvertedObjects[0], &out); err != nil {
		t.Fatalf("decoding converted object: %v", err)
	}
	if out.APIVersion != "test.io/v1beta1" || out.Kind != widgetKind {
		t.Errorf("converted object gvk = %s %s, want test.io/v1beta1 Widget", out.APIVersion, out.Kind)
	}
	if out.SizeLabel != "large" {
		t.Errorf("SizeLabel = %q, want %q (value should survive the hub round trip)", out.SizeLabel, "large")
	}
	if out.Name != "w1" {
		t.Errorf("Name = %q, want %q", out.Name, "w1")
	}
}

// A ConversionReview with no "request" field must not take the server down.
func TestServeHTTPMissingRequestDoesNotPanic(t *testing.T) {
	h := newTestHandler(t)

	rec := serve(t, h, `{"apiVersion":"apiextensions.k8s.io/v1","kind":"ConversionReview"}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestServeHTTPMalformedBodyReturnsError(t *testing.T) {
	h := newTestHandler(t)

	rec := serve(t, h, `{"this is not": `)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// An object whose GVK is not registered in the scheme must produce a failure
// response rather than dereferencing the nil GVK returned by the decoder.
func TestHandleConvertRequestUndecodableObjectReturnsFailure(t *testing.T) {
	h := newTestHandler(t)

	req := &apiextv1.ConversionRequest{
		UID:               "abc-123",
		DesiredAPIVersion: "test.io/v1beta1",
		Objects: []runtime.RawExtension{
			{Raw: []byte(`{"apiVersion":"unregistered.io/v1","kind":"Nope"}`)},
		},
	}

	var resp *apiextv1.ConversionResponse
	mustNotPanic(t, func() { resp = h.handleConvertRequest(req) })

	if resp.Result.Status != metav1.StatusFailure {
		t.Errorf("Result.Status = %q, want %q", resp.Result.Status, metav1.StatusFailure)
	}
	if len(resp.ConvertedObjects) != 0 {
		t.Errorf("expected no converted objects on failure, got %d", len(resp.ConvertedObjects))
	}
}

// Raw bytes the decoder cannot even read a GVK out of leave gvk nil, so the
// handler must not reach for gvk.Kind.
func TestHandleConvertRequestUnparseableObjectReturnsFailure(t *testing.T) {
	h := newTestHandler(t)

	req := &apiextv1.ConversionRequest{
		UID:               "abc-123",
		DesiredAPIVersion: "test.io/v1beta1",
		Objects: []runtime.RawExtension{
			{Raw: []byte(`this is not json`)},
		},
	}

	var resp *apiextv1.ConversionResponse
	mustNotPanic(t, func() { resp = h.handleConvertRequest(req) })

	if resp.Result.Status != metav1.StatusFailure {
		t.Errorf("Result.Status = %q, want %q", resp.Result.Status, metav1.StatusFailure)
	}
}

func TestHandleConvertRequestUnknownDesiredVersionFails(t *testing.T) {
	h := newTestHandler(t)

	req := &apiextv1.ConversionRequest{
		UID:               "abc-123",
		DesiredAPIVersion: "test.io/v9",
		Objects: []runtime.RawExtension{
			{Raw: []byte(`{"apiVersion":"test.io/v1alpha1","kind":"Widget","metadata":{"name":"w1"}}`)},
		},
	}

	resp := h.handleConvertRequest(req)

	if resp.Result.Status != metav1.StatusFailure {
		t.Errorf("Result.Status = %q, want %q", resp.Result.Status, metav1.StatusFailure)
	}
}

func TestConvertObject(t *testing.T) {
	h := newTestHandler(t)

	tests := []struct {
		name    string
		src     runtime.Object
		dst     runtime.Object
		wantErr string
		check   func(t *testing.T, dst runtime.Object)
	}{
		{
			name: "hub to spoke",
			src:  &hubWidget{TypeMeta: metav1.TypeMeta{APIVersion: "test.io/v1", Kind: widgetKind}, Size: "small"},
			dst:  &alphaWidget{TypeMeta: metav1.TypeMeta{APIVersion: "test.io/v1alpha1", Kind: widgetKind}},
			check: func(t *testing.T, dst runtime.Object) {
				if got := dst.(*alphaWidget).SizeName; got != "small" {
					t.Errorf("SizeName = %q, want %q", got, "small")
				}
			},
		},
		{
			name: "spoke to hub",
			src:  &alphaWidget{TypeMeta: metav1.TypeMeta{APIVersion: "test.io/v1alpha1", Kind: widgetKind}, SizeName: "medium"},
			dst:  &hubWidget{TypeMeta: metav1.TypeMeta{APIVersion: "test.io/v1", Kind: widgetKind}},
			check: func(t *testing.T, dst runtime.Object) {
				if got := dst.(*hubWidget).Size; got != "medium" {
					t.Errorf("Size = %q, want %q", got, "medium")
				}
			},
		},
		{
			name: "spoke to spoke through the hub",
			src:  &alphaWidget{TypeMeta: metav1.TypeMeta{APIVersion: "test.io/v1alpha1", Kind: widgetKind}, SizeName: "large"},
			dst:  &betaWidget{TypeMeta: metav1.TypeMeta{APIVersion: "test.io/v1beta1", Kind: widgetKind}},
			check: func(t *testing.T, dst runtime.Object) {
				if got := dst.(*betaWidget).SizeLabel; got != "large" {
					t.Errorf("SizeLabel = %q, want %q", got, "large")
				}
			},
		},
		{
			name:    "same version is rejected",
			src:     &alphaWidget{TypeMeta: metav1.TypeMeta{APIVersion: "test.io/v1alpha1", Kind: widgetKind}},
			dst:     &alphaWidget{TypeMeta: metav1.TypeMeta{APIVersion: "test.io/v1alpha1", Kind: widgetKind}},
			wantErr: "conversion is not allowed between same type",
		},
		{
			name:    "hub to non-convertible destination",
			src:     &hubWidget{TypeMeta: metav1.TypeMeta{APIVersion: "test.io/v1", Kind: widgetKind}},
			dst:     &inertWidget{TypeMeta: metav1.TypeMeta{APIVersion: "test.io/v2", Kind: widgetKind}},
			wantErr: "is not convertible",
		},
		{
			name:    "non-convertible source to hub",
			src:     &inertWidget{TypeMeta: metav1.TypeMeta{APIVersion: "test.io/v2", Kind: widgetKind}},
			dst:     &hubWidget{TypeMeta: metav1.TypeMeta{APIVersion: "test.io/v1", Kind: widgetKind}},
			wantErr: "is not convertible",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := h.convertObject(tt.src, tt.dst)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.check(t, tt.dst)
		})
	}
}

func TestGetTargetObject(t *testing.T) {
	s := newTestScheme(t)

	t.Run("registered version", func(t *testing.T) {
		obj, err := getTargetObject(s, "test.io/v1beta1", widgetKind)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := obj.(*betaWidget); !ok {
			t.Fatalf("got %T, want *betaWidget", obj)
		}
		gvk := obj.GetObjectKind().GroupVersionKind()
		if gvk.GroupVersion().String() != "test.io/v1beta1" || gvk.Kind != widgetKind {
			t.Errorf("gvk = %s, want test.io/v1beta1, Kind=Widget", gvk)
		}
	})

	t.Run("unregistered version", func(t *testing.T) {
		if _, err := getTargetObject(s, "test.io/v9", widgetKind); err == nil {
			t.Fatal("expected an error for an unregistered version, got nil")
		}
	})
}

func TestGetHubReturnsHubForSpoke(t *testing.T) {
	s := newTestScheme(t)

	hub, err := getHub(s, &alphaWidget{TypeMeta: metav1.TypeMeta{APIVersion: "test.io/v1alpha1", Kind: widgetKind}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hub == nil {
		t.Fatal("getHub returned a nil hub; spoke-to-spoke conversion cannot work without it")
	}
	if _, ok := hub.(*hubWidget); !ok {
		t.Fatalf("got %T, want *hubWidget", hub)
	}
}

func TestIsHubAndIsConvertible(t *testing.T) {
	tests := []struct {
		name            string
		obj             runtime.Object
		wantHub         bool
		wantConvertible bool
	}{
		{name: "hub type", obj: &hubWidget{}, wantHub: true, wantConvertible: false},
		{name: "spoke type", obj: &alphaWidget{}, wantHub: false, wantConvertible: true},
		{name: "inert type", obj: &inertWidget{}, wantHub: false, wantConvertible: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isHub(tt.obj); got != tt.wantHub {
				t.Errorf("isHub() = %v, want %v", got, tt.wantHub)
			}
			if got := isConvertible(tt.obj); got != tt.wantConvertible {
				t.Errorf("isConvertible() = %v, want %v", got, tt.wantConvertible)
			}
		})
	}
}

func TestStatusHelpers(t *testing.T) {
	if got := statusSucceed(); got.Status != metav1.StatusSuccess {
		t.Errorf("statusSucceed().Status = %q, want %q", got.Status, metav1.StatusSuccess)
	}

	resp := conversionResponseFailureWithMessagef("bad thing: %s", "details")
	if resp.Result.Status != metav1.StatusFailure {
		t.Errorf("Result.Status = %q, want %q", resp.Result.Status, metav1.StatusFailure)
	}
	if resp.Result.Message != "bad thing: details" {
		t.Errorf("Result.Message = %q, want %q", resp.Result.Message, "bad thing: details")
	}
}

func TestNewHandlerStartsWithEmptyScheme(t *testing.T) {
	h, err := NewHandler()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.scheme == nil {
		t.Fatal("scheme is nil")
	}
	if h.decoder == nil {
		t.Fatal("decoder is nil")
	}
	if kinds := h.scheme.AllKnownTypes(); len(kinds) != 0 {
		t.Errorf("expected an empty scheme for callers to populate, got %d kinds", len(kinds))
	}
}
