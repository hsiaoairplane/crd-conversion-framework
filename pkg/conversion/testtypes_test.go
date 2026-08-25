package conversion

import (
	"fmt"
	"testing"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

// Test API types modelling the shape a real user of this framework would have:
// one hub (storage) version, two spokes that convert through it, and one type
// that implements neither interface.
var (
	hubGV      = schema.GroupVersion{Group: "test.io", Version: "v1"}
	alphaGV    = schema.GroupVersion{Group: "test.io", Version: "v1alpha1"}
	betaGV     = schema.GroupVersion{Group: "test.io", Version: "v1beta1"}
	inertGV    = schema.GroupVersion{Group: "test.io", Version: "v2"}
	widgetKind = "Widget"
)

// hubWidget is the hub version. Size is the canonical field name.
type hubWidget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Size string `json:"size,omitempty"`
}

func (w *hubWidget) DeepCopyObject() runtime.Object {
	if w == nil {
		return nil
	}
	out := *w
	w.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	return &out
}

// Hub marks hubWidget as the conversion hub.
func (w *hubWidget) Hub() {}

// alphaWidget is a spoke that calls the field SizeName.
type alphaWidget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	SizeName string `json:"sizeName,omitempty"`
}

func (w *alphaWidget) DeepCopyObject() runtime.Object {
	if w == nil {
		return nil
	}
	out := *w
	w.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	return &out
}

func (w *alphaWidget) ConvertTo(dst conversion.Hub) error {
	hub, ok := dst.(*hubWidget)
	if !ok {
		return fmt.Errorf("unexpected hub type %T", dst)
	}
	w.ObjectMeta.DeepCopyInto(&hub.ObjectMeta)
	hub.Size = w.SizeName
	return nil
}

func (w *alphaWidget) ConvertFrom(src conversion.Hub) error {
	hub, ok := src.(*hubWidget)
	if !ok {
		return fmt.Errorf("unexpected hub type %T", src)
	}
	hub.ObjectMeta.DeepCopyInto(&w.ObjectMeta)
	w.SizeName = hub.Size
	return nil
}

// betaWidget is a second spoke that calls the field SizeLabel.
type betaWidget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	SizeLabel string `json:"sizeLabel,omitempty"`
}

func (w *betaWidget) DeepCopyObject() runtime.Object {
	if w == nil {
		return nil
	}
	out := *w
	w.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	return &out
}

func (w *betaWidget) ConvertTo(dst conversion.Hub) error {
	hub, ok := dst.(*hubWidget)
	if !ok {
		return fmt.Errorf("unexpected hub type %T", dst)
	}
	w.ObjectMeta.DeepCopyInto(&hub.ObjectMeta)
	hub.Size = w.SizeLabel
	return nil
}

func (w *betaWidget) ConvertFrom(src conversion.Hub) error {
	hub, ok := src.(*hubWidget)
	if !ok {
		return fmt.Errorf("unexpected hub type %T", src)
	}
	hub.ObjectMeta.DeepCopyInto(&w.ObjectMeta)
	w.SizeLabel = hub.Size
	return nil
}

// inertWidget implements neither conversion.Hub nor conversion.Convertible.
type inertWidget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
}

func (w *inertWidget) DeepCopyObject() runtime.Object {
	if w == nil {
		return nil
	}
	out := *w
	w.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	return &out
}

// newTestScheme registers the test types plus the apiextensions types the
// handler needs in order to decode a ConversionReview.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	s := runtime.NewScheme()
	s.AddKnownTypeWithName(hubGV.WithKind(widgetKind), &hubWidget{})
	s.AddKnownTypeWithName(alphaGV.WithKind(widgetKind), &alphaWidget{})
	s.AddKnownTypeWithName(betaGV.WithKind(widgetKind), &betaWidget{})
	s.AddKnownTypeWithName(inertGV.WithKind(widgetKind), &inertWidget{})

	for _, gv := range []schema.GroupVersion{hubGV, alphaGV, betaGV, inertGV} {
		metav1.AddToGroupVersion(s, gv)
	}

	if err := apiextv1.AddToScheme(s); err != nil {
		t.Fatalf("registering apiextensions v1: %v", err)
	}
	return s
}

// newTestHandler builds a Handler backed by the test scheme. NewHandler
// deliberately ships with an empty scheme, so tests wire one up directly.
func newTestHandler(t *testing.T) *Handler {
	t.Helper()

	s := newTestScheme(t)
	return &Handler{scheme: s, decoder: NewDecoder(s)}
}
