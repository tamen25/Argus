package objstore

import (
	"context"
	"errors"
	"testing"

	"github.com/tamen25/Argus/engine/internal/cost"
)

// StorageSource must satisfy the cost port.
var _ cost.StorageSource = StorageSource{}

type fakeLister struct {
	objs []Object
	err  error
}

func (f fakeLister) EachObject(_ context.Context, fn func(Object)) error {
	for _, o := range f.objs {
		fn(o)
	}
	return f.err
}

// StorageBytesByClass sums object sizes per storage class; an empty class name
// (S3 omits StorageClass for STANDARD) folds into STANDARD.
func TestStorageBytesByClass(t *testing.T) {
	s := StorageSource{Lister: fakeLister{objs: []Object{
		{Size: 100, StorageClass: "GLACIER_IR"},
		{Size: 50, StorageClass: ""},         // STANDARD (omitted by S3)
		{Size: 25, StorageClass: "STANDARD"}, // explicit
		{Size: 100, StorageClass: "GLACIER_IR"},
	}}}

	got, err := s.StorageBytesByClass(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got["GLACIER_IR"] != 200 {
		t.Errorf("GLACIER_IR = %d, want 200", got["GLACIER_IR"])
	}
	if got["STANDARD"] != 75 {
		t.Errorf("STANDARD = %d, want 75 (empty class folds in)", got["STANDARD"])
	}
}

func TestStorageBytesByClassPropagatesError(t *testing.T) {
	s := StorageSource{Lister: fakeLister{err: errors.New("access denied")}}
	if _, err := s.StorageBytesByClass(context.Background()); err == nil {
		t.Error("want error from lister")
	}
}
